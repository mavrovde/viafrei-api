#!/usr/bin/env bash
# parity-shards.sh OUT [N]
#
# The store's parity set as CI runs it (the parity-shard and parity jobs of
# ci.yml), on this machine: N shards (default 4) side by side, each into a
# profile and a -parity.ran file of its own, then judged together by
# paritygather and merged into OUT. A shard that fails, or a statement no
# shard proves, fails the script; the shards' logs are kept beside OUT.
set -euo pipefail

out=${1:?usage: parity-shards.sh OUT [N]}
n=${2:-4}
dir=$(dirname "$out")/parity-shards
rm -rf "$dir"
mkdir -p "$dir"

pids=()
for k in $(seq 1 "$n"); do
  go test -count=1 -tags integration -covermode=atomic -coverpkg=./... -coverprofile="$dir/coverage-parity-$k.out" \
    ./internal/adapters/store -args -parity -parity.shard="$k/$n" -parity.ran="$PWD/$dir/parity-ran-$k.json" \
    >"$dir/shard-$k.log" 2>&1 &
  pids+=("$!")
done
failed=0
for k in $(seq 1 "$n"); do
  if ! wait "${pids[$((k - 1))]}"; then
    echo "parity-shards: shard $k/$n failed; its log: $dir/shard-$k.log" >&2
    failed=1
  fi
done
if [[ $failed -ne 0 ]]; then
  exit 1
fi

ran=() profiles=()
for k in $(seq 1 "$n"); do
  ran+=("$dir/parity-ran-$k.json")
  profiles+=("$dir/coverage-parity-$k.out")
done
go run ./internal/testkit/paritygather "${ran[@]}"
go run ./internal/testkit/covergate -min 0 -exclusions test/coverage-exclusions.txt -o "$out" "${profiles[@]}" >/dev/null
