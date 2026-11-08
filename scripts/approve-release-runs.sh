#!/usr/bin/env bash
# approve-release-runs.sh BRANCH
#
# The release pull request, and the version-sync pull request
# (platform-sync.yml), are opened by github-actions[bot], and GitHub holds
# the pull_request runs of CI and Security for it as "action_required" until
# they are approved. Branch protection reads only the check runs of those
# pull_request runs (a workflow_dispatch run on the same commit is not
# associated with the pull request), so this script approves them. It waits
# until both workflows have a pull_request run on the branch's head commit,
# approves each one that waits for approval, and fails when a workflow has no
# run on that commit by the deadline (TST-011: no run is no evidence).
set -euo pipefail

branch=${1:?usage: approve-release-runs.sh BRANCH}
[[ $branch == release-please--* || $branch == platform-sync/* ]] || { echo "FAIL: '$branch' is neither a release-please nor a platform-sync branch" >&2; exit 2; }
: "${GH_REPO:?GH_REPO must name the repository}"
deadline=$((SECONDS + ${APPROVE_WAIT_S:-180}))

sha=$(gh api "repos/$GH_REPO/branches/$branch" --jq .commit.sha)
[[ $sha =~ ^[0-9a-f]{40}$ ]] || { echo "FAIL: no head commit for $branch" >&2; exit 2; }
echo "release branch $branch at $sha"

for wf in CI Security; do
  while :; do
    runs=$(gh api "repos/$GH_REPO/actions/runs?event=pull_request&head_sha=$sha&per_page=100" \
      --jq "[.workflow_runs[] | select(.name == \"$wf\") | {id, status, conclusion}]")
    [[ $(jq length <<<"$runs") -gt 0 ]] && break
    (( SECONDS < deadline )) || { echo "FAIL: no pull_request run of $wf on $sha by the deadline" >&2; exit 1; }
    sleep 10
  done
  for id in $(jq -r '.[] | select(.conclusion == "action_required") | .id' <<<"$runs"); do
    gh api -X POST "repos/$GH_REPO/actions/runs/$id/approve" >/dev/null
    echo "approved $wf run $id"
  done
  jq -r --arg wf "$wf" '.[] | "\($wf) run \(.id): \(.status) \(.conclusion // "")"' <<<"$runs"
done
