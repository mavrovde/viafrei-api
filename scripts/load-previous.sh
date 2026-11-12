#!/usr/bin/env bash
# load-previous.sh DIR
#
# Fetches the figures of the newest successful nightly load run on main
# (TST-021, issue #80; docs/load-tests.md) into DIR, as DIR/load-report.json,
# for the runner's -previous. It reads the repository named by REPO with the
# gh CLI and the token in GH_TOKEN, and prints which run it fetched.
#
# When main has no successful nightly run yet, or that run's artifact holds
# no figures (a run from before they were written), it writes nothing and
# says so: the runner then reports the file as absent, by name, and judges
# the run on the absolute budgets alone. A run list or a download that fails
# is an error, exit 1, and never read as "no previous run".
#
# Exit 0 when the figures were fetched or there are none to fetch, 1 when
# GitHub could not be read, 2 on a usage error.
set -euo pipefail

dir=${1:-}
[[ -n $dir && $# -eq 1 ]] || { echo "usage: load-previous.sh DIR" >&2; exit 2; }
[[ -n ${REPO:-} ]] || { echo "FAIL: REPO must name the repository (owner/name)" >&2; exit 2; }
gh=${GH:-gh}

if ! id=$("$gh" run list -R "$REPO" --workflow load-nightly.yml --branch main --status success --limit 1 \
  --json databaseId --jq '.[0].databaseId // empty'); then
  echo "FAIL: the nightly load runs of main could not be listed" >&2
  exit 1
fi
if [[ -z $id ]]; then
  echo "load-previous: main has no successful nightly load run; nothing to compare with"
  exit 0
fi
if [[ ! $id =~ ^[0-9]+$ ]]; then
  echo "FAIL: the run list answered something that is not a run id" >&2
  exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
if ! "$gh" run download "$id" -R "$REPO" -n load-nightly -D "$tmp"; then
  echo "FAIL: the artifact of nightly load run $id could not be downloaded" >&2
  exit 1
fi
if [[ ! -f $tmp/load-report.json ]]; then
  echo "load-previous: nightly load run $id left no figures (load-report.json); nothing to compare with"
  exit 0
fi
mkdir -p "$dir"
cp "$tmp/load-report.json" "$dir/load-report.json"
echo "load-previous: the figures of nightly load run $id are in $dir/load-report.json"
