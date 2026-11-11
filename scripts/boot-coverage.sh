#!/usr/bin/env bash
# boot-coverage.sh OUT
#
# The boot smoke for coverage (TST-001): builds every main package of the
# module with -cover, runs each binary once the way an operator would, and
# writes the counters as a text profile to OUT for the coverage gate. It is
# the only thing that reaches func main() itself; everything below main is
# covered by the unit and integration tests.
#
# A main package without an invocation below is a failure, never a skip, so a
# new command cannot slip past the 100 % rule unseen.
set -euo pipefail

out=${1:?usage: boot-coverage.sh OUT}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/cov" "$work/run"
export GOCOVERDIR="$work/cov"

module=$(go list -m)

# expect CODE CMD... runs CMD and fails unless it exits with CODE.
expect() {
  local want=$1 got=0
  shift
  "$@" >"$work/run/stdout" 2>"$work/run/stderr" || got=$?
  if [[ $got != "$want" ]]; then
    echo "boot-coverage: $* exited $got, want $want" >&2
    cat "$work/run/stderr" >&2
    exit 1
  fi
}

invoke() {
  local pkg=$1 bin=$2
  case "$pkg" in
    cmd/viafrei-api)
      expect 0 "$bin" version
      ;;
    cmd/fixturegen)
      expect 0 "$bin" -profile tiny -seed 1 -out "$work/run/fixtures"
      ;;
    cmd/reqreport)
      expect 2 "$bin"
      ;;
    internal/testkit/gotestsum-lite)
      echo '{"Action":"pass","Package":"p","Test":"TestBoot"}' >"$work/run/pass.json"
      expect 0 "$bin" <"$work/run/pass.json"
      ;;
    internal/testkit/covergate)
      expect 2 "$bin"
      ;;
    internal/testkit/mutgate)
      expect 2 "$bin"
      ;;
    internal/testkit/loadrun)
      expect 2 "$bin"
      ;;
    internal/release/cigate)
      expect 2 "$bin"
      ;;
    internal/release/platformsync)
      expect 2 "$bin"
      ;;
    *)
      echo "boot-coverage: main package $pkg has no invocation in $0" >&2
      exit 1
      ;;
  esac
}

n=0
while IFS= read -r pkg; do
  rel=${pkg#"$module"/}
  bin="$work/bin/$(basename "$rel")"
  go build -cover -covermode=atomic -coverpkg=./... -o "$bin" "./$rel"
  invoke "$rel" "$bin"
  n=$((n + 1))
done < <(go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./...)

if [[ $n -eq 0 ]]; then
  echo "boot-coverage: found no main package; nothing was measured" >&2
  exit 1
fi
go tool covdata textfmt -i="$GOCOVERDIR" -o "$out"
if [[ $(wc -l <"$out") -le 1 ]]; then
  echo "boot-coverage: $out holds no coverage block" >&2
  exit 1
fi
echo "boot-coverage: $n main packages run, profile in $out"
