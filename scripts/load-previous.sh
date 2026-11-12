#!/usr/bin/env bash
# load-previous.sh DIR PROFILE
#
# Fetches the figures of the newest successful nightly load run on main of
# the PROFILE budget (pr or nightly; TST-021, issue #80; docs/load-tests.md)
# into DIR, as DIR/load-report.json, for the runner's -previous. It reads
# the repository named by REPO with the gh CLI and the token in GH_TOKEN,
# and prints which run it fetched.
#
# It looks at the newest 10 successful runs, newest first, and takes the
# first whose figures are of PROFILE: a dispatched run of the other budget
# is passed over, by name. It writes nothing, says why and exits 0 when:
#   - main has no successful nightly run;
#   - the run it reaches has no figures (a run from before they were
#     written; the older ones have none either);
#   - the run it reaches has an expired artifact, or none (retention; the
#     older ones are older still);
#   - none of the runs looked at is of PROFILE.
# The runner then reports the file as absent, by name, and judges the run
# on the absolute budgets alone. A run list, an artifact list or a download
# that fails, and an answer that is not a run id, are errors, exit 1, and
# never read as "no previous run".
#
# Exit 0 when the figures were fetched or there are none to fetch, 1 when
# GitHub could not be read, 2 on a usage error.
set -euo pipefail

dir=${1:-}
profile=${2:-}
[[ -n $dir && $# -eq 2 && ($profile == pr || $profile == nightly) ]] || {
  echo "usage: load-previous.sh DIR pr|nightly" >&2
  exit 2
}
[[ -n ${REPO:-} ]] || { echo "FAIL: REPO must name the repository (owner/name)" >&2; exit 2; }
gh=${GH:-gh}
limit=10

if ! ids=$("$gh" run list -R "$REPO" --workflow load-nightly.yml --branch main --status success --limit "$limit" \
  --json databaseId --jq '.[].databaseId'); then
  echo "FAIL: the nightly load runs of main could not be listed" >&2
  exit 1
fi
if [[ -z $ids ]]; then
  echo "load-previous: main has no successful nightly load run; nothing to compare with"
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for id in $ids; do
  if [[ ! $id =~ ^[0-9]+$ ]]; then
    echo "FAIL: the run list answered something that is not a run id" >&2
    exit 1
  fi
  if ! expired=$("$gh" api "repos/$REPO/actions/runs/$id/artifacts" \
    --jq '[.artifacts[] | select(.name == "load-nightly") | .expired | tostring][0] // "none"'); then
    echo "FAIL: the artifacts of nightly load run $id could not be listed" >&2
    exit 1
  fi
  case $expired in
    true)
      echo "load-previous: the artifact of nightly load run $id has expired; nothing to compare with"
      exit 0 ;;
    none)
      echo "load-previous: nightly load run $id has no load-nightly artifact; nothing to compare with"
      exit 0 ;;
    false) ;;
    *)
      echo "FAIL: the artifact list of nightly load run $id answered neither expired nor not" >&2
      exit 1 ;;
  esac
  if ! "$gh" run download "$id" -R "$REPO" -n load-nightly -D "$tmp/$id"; then
    echo "FAIL: the artifact of nightly load run $id could not be downloaded" >&2
    exit 1
  fi
  if [[ ! -f $tmp/$id/load-report.json ]]; then
    echo "load-previous: nightly load run $id left no figures (load-report.json); nothing to compare with"
    exit 0
  fi
  # A file that is not readable figures is the runner's to judge (it fails
  # the run): it is fetched as it is.
  got=$(jq -r '.profile // empty' "$tmp/$id/load-report.json" 2>/dev/null || true)
  if [[ -n $got && $got != "$profile" ]]; then
    echo "load-previous: nightly load run $id is of the $got budget, not $profile; looking at an older run"
    continue
  fi
  mkdir -p "$dir"
  cp "$tmp/$id/load-report.json" "$dir/load-report.json"
  echo "load-previous: the figures of nightly load run $id ($profile budget) are in $dir/load-report.json"
  exit 0
done
echo "load-previous: none of the newest $limit successful nightly load runs of main is of the $profile budget; nothing to compare with"
