#!/usr/bin/env bash
# sarif-docker.sh NAME PATH -- DOCKER-RUN-ARGS...
#
# Runs one scanner container (docker run DOCKER-RUN-ARGS, given a name of its
# own), copies the SARIF report the scanner wrote at PATH inside the
# container to sarif/NAME.sarif, lists its results and exits with the
# scanner's own exit code, so the scanner still decides pass or fail. No host
# directory is shared for the report: it is copied out of the stopped
# container. A scanner that wrote no readable SARIF is a failure even when it
# exited 0, because a scan that left no report proves nothing (TST-011).
set -uo pipefail

name=${1:?usage: sarif-docker.sh NAME PATH -- DOCKER-RUN-ARGS...}
path=${2:?usage: sarif-docker.sh NAME PATH -- DOCKER-RUN-ARGS...}
[[ ${3:-} == -- ]] || { echo "usage: sarif-docker.sh NAME PATH -- DOCKER-RUN-ARGS..." >&2; exit 2; }
shift 3

cid="sarif-${name}-$$"
mkdir -p sarif
out="sarif/${name}.sarif"

docker run --name "$cid" "$@"
code=$?
docker cp "$cid:$path" "$out" >/dev/null 2>&1
docker rm --force "$cid" >/dev/null 2>&1 || true

if ! jq -e '.runs | type == "array"' "$out" >/dev/null 2>&1; then
  echo "FAIL: $name wrote no readable SARIF at $path (scanner exit $code)" >&2
  rm -f "$out"
  exit $(( code == 0 ? 1 : code ))
fi
n=$(jq '[.runs[].results[]?] | length' "$out")
echo "$name: exit $code, $n SARIF result(s) in $out"
jq -r '.runs[].results[]? | "  \(.ruleId // "-") \(.locations[0].physicalLocation.artifactLocation.uri // "-"):\(.locations[0].physicalLocation.region.startLine // "-")"' "$out"
exit "$code"
