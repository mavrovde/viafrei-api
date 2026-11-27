#!/usr/bin/env bash
# approve-release-runs.sh BRANCH
#
# The release pull request is opened by github-actions[bot], and GitHub holds
# the pull_request runs of CI and Security for it as "action_required" until
# they are approved. Branch protection reads only the check runs of those
# pull_request runs (a workflow_dispatch run on the same commit is not
# associated with the pull request), so this script approves them.
#
# For each workflow it lists the runs on the branch's head commit
# (actions/runs?head_sha=...) and keeps those of that workflow's name and of
# the pull_request event, asking again on every round until one is there:
# GitHub can take minutes to list the runs of a new pull request. It approves
# each one that waits for approval, and fails when a workflow has no run on
# that commit by the deadline, APPROVE_WAIT_S seconds (default 600) after the
# start (TST-011: no run is no evidence). A failed read is reported and asked
# again; the deadline still holds.
set -euo pipefail

branch=${1:?usage: approve-release-runs.sh BRANCH}
[[ $branch == release-please--* ]] || { echo "FAIL: '$branch' is not a release-please branch" >&2; exit 2; }
: "${GH_REPO:?GH_REPO must name the repository}"
deadline=$((SECONDS + ${APPROVE_WAIT_S:-600}))
poll=${APPROVE_POLL_S:-10}
errf=$(mktemp)
trap 'rm -f "$errf"' EXIT
last_err=""

sha=$(gh api "repos/$GH_REPO/branches/$branch" --jq .commit.sha)
[[ $sha =~ ^[0-9a-f]{40}$ ]] || { echo "FAIL: no head commit for $branch" >&2; exit 2; }
echo "release branch $branch at $sha"

for wf in CI Security; do
  while :; do
    if runs=$(gh api "repos/$GH_REPO/actions/runs?head_sha=$sha&per_page=100" \
      --jq "[.workflow_runs[] | select(.name == \"$wf\" and .event == \"pull_request\") | {id, status, conclusion}]" 2>"$errf"); then
      last_err="" # a later read answered: the deadline is not that read's
      [[ $(jq length <<<"$runs") -gt 0 ]] && break
    else
      last_err=$(tail -n 1 "$errf")
      echo "the runs of $sha could not be read ($last_err); asking again" >&2
    fi
    (( SECONDS < deadline )) || { echo "FAIL: no pull_request run of $wf on $sha by the deadline${last_err:+; the last read failed: $last_err}" >&2; exit 1; }
    sleep "$poll"
  done
  for id in $(jq -r '.[] | select(.conclusion == "action_required") | .id' <<<"$runs"); do
    gh api -X POST "repos/$GH_REPO/actions/runs/$id/approve" >/dev/null
    echo "approved $wf run $id"
  done
  jq -r --arg wf "$wf" '.[] | "\($wf) run \(.id): \(.status) \(.conclusion // "")"' <<<"$runs"
done
