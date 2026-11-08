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
   built-in `GITHUB_TOKEN`. GitHub starts no `pull_request` workflow for a
   push made with that token, so the same job then dispatches `ci.yml` and
   `security.yml` on the release branch (`workflow_dispatch`, the one event
   that token may start); their check runs land on the pull request's head
   commit, where branch protection reads them. It bumps `version.txt`, updates
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
5. The same run calls `.github/workflows/release.yml`, which:
   - checks the tag is annotated and well-formed, and that its commit lies on
     `main` or a `hotfix/*` branch (`git merge-base --is-ancestor`); for a
     hotfix branch, also that the hotfix ruleset protects it (see Hotfix)
   - runs `scripts/ci-gate.sh` on the tagged commit again, before anything is
     pushed or signed (this also covers a release-candidate tag pushed by hand)
   - refuses, before anything is built, when the release of that tag is
     already published or the image `:X.Y.Z` already exists: a re-run never
     builds a second digest, never moves `:X.Y.Z` or `:sha-<12>`, and never
     appends the notes twice
   - builds `linux/amd64` and `linux/arm64`, pushes to
     `ghcr.io/mavrovde/viafrei-api` as `:X.Y.Z` and `:sha-<12>`, plus `:X.Y`
     and `:latest` for a final release (`:X.Y` only when it is the highest
     patch of its minor, `:latest` only when it is the highest version, so a
     backported patch never moves either back)
   - signs the digest with cosign (keyless) and attests SLSA build provenance
     and the SPDX SBOM to the image
   - attaches `openapi.json`, `openapi-changelog.txt`, `openapi-breaking.txt`
     (oasdiff against the previous release), `sbom.spdx.json`,
     `sbom.cdx.json` and `SHA256SUMS`
   - appends the digest, the tags, the verify commands and the milestone link
     to the notes, marks every 0.x and `-rc` release as a pre-release, and
     publishes the draft
6. The `verify` job of the same run pulls the image by digest, runs
   `cosign verify` and `gh attestation verify`, checks `SHA256SUMS` against
   the downloaded assets, and smoke-tests `/healthz` of the published image.
   It then closes the milestone whose title starts with `X.Y.Z` if no issue in
   it is open, or lists the open issues in the job summary.

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

### First public release

At the "First public release" milestone the API version switches to the
platform-synced version (issue #6). The switch is one commit on `main` with a
`Release-As` footer, for example:

```text
chore: switch to the platform-synced version

Release-As: <the platform version, X.Y.Z>
```

release-please then proposes exactly that version. The sync itself is not
built yet; until it is, no `Release-As` footer is used.

## Checklist for the release pull request

- [ ] CI is green on the release pull request, including the image and compose jobs.
- [ ] The Coverage gate job shows 100 % for every package, and every line of `test/coverage-exclusions.txt` carries its reason ([coverage](coverage.md)).
- [ ] The `Requirement report` job is green on `main` at the release commit; its `requirement-report` artifact holds the requirement report and the draft test completion report, which the maintainers complete and attach to the release (TEST-002).
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

A maintainer creates an rc tag by hand on a commit of `main`:

```sh
git tag -a v0.3.0-rc.1 -m "Release candidate v0.3.0-rc.1" <sha>
git push origin v0.3.0-rc.1
```

The tag push runs `release.yml`: image `:0.3.0-rc.1`, no `:X.Y`, no `:latest`,
a pre-release page with generated notes. Candidates are never promoted by
re-tagging; the final release is cut by release-please as usual.

## Tags

- Only annotated tags `vX.Y.Z` and `vX.Y.Z-rc.N`. `release.yml` refuses a
  lightweight tag.
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
