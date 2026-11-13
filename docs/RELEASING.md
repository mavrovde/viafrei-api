# Releasing

This page is the maintainer checklist for releases of ViaFrei API. The design
behind it is in the wiki: [Release Process](https://github.com/mavrovde/viafrei-api/wiki/Release-Process),
[Compatibility and Versioning](https://github.com/mavrovde/viafrei-api/wiki/Spec-Compatibility-and-Versioning)
and [CI/CD and Release](https://github.com/mavrovde/viafrei-api/wiki/Spec-CI-CD-and-Release).

Release maintainers: **@mavrovde** and **@ibragins**. Only they create `v*` tags
and merge release pull requests.

## How a release happens

1. Every change reaches `main` through a pull request whose title is a
   [Conventional Commit](https://www.conventionalcommits.org/en/v1.0.0/)
   (`feat:`, `fix:`, `docs:`, `ci:`, `deps:`, … and `!` or a
   `BREAKING CHANGE:` footer for a breaking change). The title becomes the
   squash commit and the changelog line.
2. Once CI has passed on a push to `main`, [release-please](https://github.com/googleapis/release-please)
   (`.github/workflows/release-please.yml`, started by `workflow_run`) opens
   or updates the **release pull request** `chore: release X.Y.Z` with the
   built-in `GITHUB_TOKEN`. GitHub holds the pull request's CI and Security
   runs as "action_required" because github-actions[bot] opened it, and
   branch protection reads only pull-request runs (a `workflow_dispatch` run
   on the same commit is not associated with the pull request), so the same
   job approves them (`scripts/approve-release-runs.sh`, which fails when
   either workflow has no run on the head commit). It bumps `version.txt`, updates
   `.release-please-manifest.json` and adds the section to `CHANGELOG.md`
   ([Keep a Changelog](https://keepachangelog.com/en/1.1.0/) headings).
3. A maintainer reviews and merges the release pull request (checklist below).
4. release-please writes a **draft** release, which creates no tag. The `tag`
   job runs `scripts/ci-gate.sh` on the release commit: the check runs
   `Unit and handler tests`, `Integration tests (PostGIS)`,
   `Coverage gate (100 % per package)`, `Image (build, lint, scan, healthz)`,
   `govulncheck` and `Lint` must all have **completed with
   success** on that commit, and a missing, queued, running or failed one is
   a refusal (REL-015). Only a check run that GitHub Actions created inside a
   run of `.github/workflows/ci.yml` on that commit counts: any app or
   workflow holding `checks: write` can create a check run of any name, so a
   same-named run from elsewhere is not evidence. Only then does it create
   the **annotated** tag `vX.Y.Z` on the release commit and push it.
5. The same run calls `.github/workflows/release.yml`, the only workflow
   that pushes an image. Images exist for **confirmed releases only**: a push
   to `main` and a release candidate publish none. The workflow:
   - checks the tag is an annotated `vX.Y.Z` with no suffix, that it equals
     `version.txt` at the tagged commit, and that its commit lies on
     `main` or a `hotfix/*` branch (`git merge-base --is-ancestor`); for a
     hotfix branch, also that the hotfix ruleset protects it (see Hotfix)
   - runs `scripts/ci-gate.sh` on the tagged commit again, before anything is
     pushed or signed (this also covers a `vX.Y.Z` tag pushed by hand)
   - refuses, before anything is built, when the release of that tag is
     already published or the image `:X.Y.Z` already exists: a re-run never
     builds a second digest, never moves `:X.Y.Z`, and never
     appends the notes twice
   - builds `linux/amd64` and `linux/arm64`, pushes to
     `ghcr.io/mavrovde/viafrei-api` as `:X.Y.Z`, plus `:X.Y` and `:latest`
     (`:X.Y` only when it is the highest patch of its minor, `:latest` only
     when it is the highest version, so a backported patch never moves
     either back)
   - signs the digest with cosign (keyless) and attests SLSA build provenance
     and the SPDX SBOM to the image
   - attaches `openapi.json`, `openapi-changelog.txt`, `openapi-breaking.txt`
     (oasdiff against the previous release), `sbom.spdx.json`,
     `sbom.cdx.json` and `SHA256SUMS`
   - appends the digest, the tags, the verify commands and the milestone link
     to the notes, marks every 0.x release as a pre-release, and
     publishes the draft
6. The `verify` job of the same run pulls the image by digest, runs
   `cosign verify` and `gh attestation verify`, checks `SHA256SUMS` against
   the downloaded assets, and smoke-tests `/healthz` of the published image.
   It then closes the milestone whose title starts with `X.Y.Z` if no issue in
   it is open, or lists the open issues in the job summary.
7. The `pages` job builds the documentation site from the tag
   (`scripts/docs-build.sh`: the guide from `README.md` and the guide pages
   under `docs/`, the endpoints page generated from `api/openapi.json` with its
   recorded examples, the changelog, the OpenAPI reference rendered from
   `api/openapi.json`, and the document itself) and deploys it to GitHub Pages,
   <https://mavrovde.github.io/viafrei-api/>, then reads the published
   `index.html`, `reference.html` and `openapi.json` back and fails unless
   they serve that version. Only the newest release publishes; a backported
   patch never replaces the site. CI builds the same site on
   every pull request (`OpenAPI lint and breaking changes` job) and checks
   its links, diagrams and accessibility (`Documentation portal` job,
   [accessibility](accessibility.md)).

### When CI failed on the release commit

The `tag` job refuses and nothing is tagged or published. If the failure was
not in the code (a flaky runner, a registry outage), re-run the failed jobs
of the CI run on the release commit (`gh run rerun <ci-run-id> --failed`);
once it has succeeded, re-run the failed jobs of the Release Please run
(`gh run rerun <release-please-run-id> --failed`), whose `tag` job runs the
gate again. If the code is at fault, fix it through a pull request and
release the next patch; the draft release of the refused version is deleted
by hand (it never had a tag).

### When the release run failed part-way

A re-run of `release.yml` is accepted only while the release page is still a
draft and `:X.Y.Z` does not exist. Once the image was pushed, the run is
refused by design; release the next patch instead.

### Version rules

| Change since the last release | Before 1.0.0 | From 1.0.0 |
|---|---|---|
| `fix:` | patch | patch |
| `feat:` | **minor** | minor |
| breaking (`!` / `BREAKING CHANGE:`) | **minor** | major |

Pre-1.0 releases are always pre-releases on GitHub. The OpenAPI diff flags a
breaking change on the release page even when the version rules allow it.

### Version sync with the platform

From the "First public release" milestone on, the API's release follows the
ViaFrei platform's release (REL-001), the way the MCP server's does. The
sync is `.github/workflows/platform-sync.yml`, daily and by hand, built on
`go run ./internal/release/platformsync`:

1. `platformsync read` reads the platform's current release from a public
   source only: the npm dist-tag `latest` of the package `viafrei` (default)
   or the latest release of `mavrovde/viafrei-mcp` (`-source github`). One
   read-only request within `-timeout` (10 s in the workflow); an answer that is not one release version
   `X.Y.Z` (an error, a draft or pre-release, unreadable JSON) fails the run
   with exit 2, never a guess.
2. `platformsync apply -version X.Y.Z` writes the version to
   `internal/buildinfo/platform-version.txt` and the commit message of the
   sync: the subject `deps: keep in step with ViaFrei platform X.Y.Z` (the
   CHANGELOG line, under Dependencies) and, once the API's release follows
   the platform's, the footer `Release-As: X.Y.Z`. The release follows from
   the run made with `-start` on: while the API is a 0.x pre-release, only
   that run writes the footer, and any other moved pin writes none, so
   release-please goes on with the API's own version. It never goes back: a
   version older than the synced one or than
   `version.txt` is refused (exit 1).
3. The workflow pushes `platform-sync/X.Y.Z`, opens the pull request
   assigned to a maintainer, and approves its CI and Security runs. A
   maintainer reviews and squash-merges it; the squash keeps the footer, when there is one.
   The pull request also pins the parity bundle of `X.Y.Z` in
   `test/parity/lock.tsv` ([parity](parity.md)): with none pinned the parity
   harness fails closed, so the sync cannot merge without one.
4. release-please then proposes exactly `X.Y.Z`: the release pull request
   bumps `version.txt`, `.release-please-manifest.json` and `CHANGELOG.md` in
   one commit, and the tag job tags that commit `vX.Y.Z` as for every release.

`/v1/meta` reports `platformVersion` from that file (null before the first
sync) beside `apiVersion` and `contractVersion`. A deployment checks the
running service against the platform's release and read-contract version
with a subcommand that needs no shell in the image:

```sh
viafrei-api meta-check -platform X.Y.Z -contract M.m   # 0 match, 1 mismatch (both values named), 2 unreadable
```

Before the first public release the file says `unsynced` and the scheduled
run only reports the platform's version. The switch is one manual run of the
workflow with `start: true` at that milestone; until then no `Release-As`
footer is used, also when the pinned platform version moves (the parity
bundle of a platform release is pinned before the first public release).

Try the sync against a fixture version, with no network:

```sh
go run ./internal/release/platformsync apply -version 2.5.21 -start -message /tmp/msg
go run ./cmd/viafrei-api version   # "platform":"2.5.21"
git checkout internal/buildinfo/platform-version.txt
```

## Checklist for the release pull request

- [ ] CI is green on the release pull request, including the image and compose jobs.
- [ ] The Coverage gate job shows 100 % for every package, and every line of `test/coverage-exclusions.txt` carries its reason ([coverage](coverage.md)).
- [ ] The `Requirement report` job is green on `main` at the release commit; its `requirement-report` artifact holds the requirement report and the draft test completion report, which the maintainers complete and attach to the release (TEST-002).
- [ ] For a minor release (`X.Y.0`): the manual accessibility pass of the documentation portal is done on the portal built from the release commit, and its result is recorded in section 6 of the test completion report ([accessibility](accessibility.md#manual-pass-once-per-minor-release)). The automated check (`Documentation portal` job) is green on every release.
- [ ] The CHANGELOG section reads correctly; every user-visible change has a line.
- [ ] Every breaking change is intended and is described with a migration note.
- [ ] `api/openapi.json` on `main` is the contract you mean to publish.
- [ ] The milestone's remaining open issues are moved or closed (otherwise it stays open and the run reports them).
- [ ] A second maintainer has approved.
- [ ] Merge with **squash**, keeping the title `chore: release X.Y.Z`.

After the run:

- [ ] The release page shows the digest, the tags, the assets and the verify commands.
- [ ] The `verify` job is green.
- [ ] `docker pull ghcr.io/mavrovde/viafrei-api:X.Y.Z` works from a clean machine.

## Release candidates

A release candidate publishes nothing: no image, no release page.
`release.yml` starts only on a `vX.Y.Z` tag push and refuses any other tag,
because an image exists for a confirmed release only. A candidate is the
commit itself: CI's `Image (build, lint, scan, healthz)` job builds, lints,
scans and smoke-tests the image of every commit on `main` without pushing
it, and the documentation portal of every pull request is checked the same
way. To try a candidate, build it locally from that commit
(`docker build --target runtime .`) or run the Compose stack.

## Tags

- Only annotated tags `vX.Y.Z`. `release.yml` refuses a lightweight tag, a
  tag with a suffix (`-rc.N` or any other) and a tag that differs from
  `version.txt`. Tags `vX.Y.Z-rc.N` made before 0.3.0 stay as history.
- A tag is never moved and never deleted. A broken release is followed by a
  new patch release.
- Only release maintainers and the release workflow create `v*` tags.

### Tag ruleset (documented here, applied by a repository admin)

These calls protect `v*` tags. They are **not** applied by any workflow; a
repository admin runs them once. Bypass is granted to repository admins (role
id 5) and to the GitHub Actions app (integration id 15368), which pushes the
tag in `release-please.yml`.

```sh
gh api -X POST repos/mavrovde/viafrei-api/rulesets --input - <<'JSON'
{
  "name": "release tags",
  "target": "tag",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["refs/tags/v*"], "exclude": [] } },
  "bypass_actors": [
    { "actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always" },
    { "actor_id": 15368, "actor_type": "Integration", "bypass_mode": "always" }
  ],
  "rules": [
    { "type": "creation" },
    { "type": "update" },
    { "type": "deletion" },
    { "type": "non_fast_forward" }
  ]
}
JSON
```

Check it:

```sh
gh api repos/mavrovde/viafrei-api/rulesets --jq '.[] | select(.target == "tag") | {id, name, enforcement}'
```

With `update` and `deletion` restricted, not even a maintainer moves a tag
without first changing the ruleset, which is the point.

## Rollback

Never by moving or deleting a tag. Every release is an immutable digest.

1. Find the previous good release on its release page and copy its digest.
2. Redeploy that digest (`ghcr.io/mavrovde/viafrei-api@sha256:…`) in the
   deployment configuration. Deployments reference digests, not tags.
3. Open an issue for the defect; the fix ships as a new patch release.

## Hotfix

For a fix that must not wait for what is already on `main`. A hotfix tag is
released only from a `hotfix/*` branch that the hotfix ruleset protects:
`release.yml` reads the branch's rules through the API and refuses unless
they require a pull request with at least one approving review and forbid
force pushes, because anyone with write access can push an unprotected
branch.

What this check cannot see, so the ruleset's owner keeps it true:

- **Bypass actors.** The rules API lists the rules that apply to the branch,
  not who may bypass them. Anyone in the ruleset's bypass list (an admin, a
  team, an app) can still push to the branch directly; keep that list empty
  for the hotfix ruleset, or as short as the tag ruleset's.
- **Time of check.** The rules are read when `release.yml` runs, not when
  each commit landed. A ruleset switched off, weakened or created after the
  fact is not detected for commits pushed while it did not hold; changes to
  rulesets are visible in the repository's audit log.

1. Branch `hotfix/X.Y.Z+1` from the tag `vX.Y.Z`.
2. Land the fix there through a pull request (`fix:` title), with review.
   CI runs on every push to `hotfix/**`, so the merged head has the check
   runs the release gate requires.
3. A maintainer creates the annotated tag `vX.Y.(Z+1)` on that branch's
   head and pushes it; `release.yml` publishes it from the tag push.
4. Merge or cherry-pick the fix into `main` and add the version to
   `.release-please-manifest.json` there, so release-please continues from it.

### Hotfix ruleset (documented here, applied by a repository admin)

Applied once, like the tag ruleset; no workflow applies it.

```sh
gh api -X POST repos/mavrovde/viafrei-api/rulesets --input - <<'JSON'
{
  "name": "hotfix branches",
  "target": "branch",
  "enforcement": "active",
  "conditions": { "ref_name": { "include": ["refs/heads/hotfix/**"], "exclude": [] } },
  "rules": [
    { "type": "pull_request", "parameters": {
        "required_approving_review_count": 1,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": false,
        "require_last_push_approval": true,
        "required_review_thread_resolution": true } },
    { "type": "non_fast_forward" },
    { "type": "deletion" }
  ]
}
JSON
```

Check what `release.yml` will see for a branch:

```sh
gh api repos/mavrovde/viafrei-api/rules/branches/hotfix/X.Y.Z --jq '[.[].type]'
```

## Yanking a release

A release is never deleted, because people may already depend on its digest.
To withdraw one:

1. Edit the release page: prefix the title with `[YANKED]` and put the reason
   and the replacement version at the top of the notes.
2. If it carried `:latest` or `:X.Y`, publish the fix so the moving tags
   point to a good image again (`release.yml` refuses to rebuild a published
   release).
3. Record the yank in `CHANGELOG.md` with the next release.
4. For a security reason, publish a GitHub security advisory.

## Required secrets and settings

| Item | Used by | Without it |
|---|---|---|
| `GITHUB_TOKEN` (built in) | every workflow | — |
| `SONAR_TOKEN` | CI `sonar` job | the job fails and names the secret |
| `SNYK_TOKEN` | security workflow | the job fails and names the secret |
| `SONAR_TOKEN` and `SNYK_TOKEN` again as **Dependabot secrets** (Settings > Secrets and variables > Dependabot; **the owner adds them**) | the same jobs on a Dependabot pull request, which reads Dependabot secrets and never Actions secrets | both jobs fail on every Dependabot pull request, saying that the Dependabot secret is missing |
| Workflow permission "Allow GitHub Actions to create and approve pull requests" (on) | release-please opens the release pull request with `GITHUB_TOKEN` | no release pull request: the job fails with "GitHub Actions is not permitted to create or approve pull requests" |
| GHCR package visibility public, linked to this repository | image pulls | anonymous pulls fail |
| GitHub Pages on, source "GitHub Actions"; the `github-pages` environment deploys from `main` | release `pages` job | the deploy step fails |
