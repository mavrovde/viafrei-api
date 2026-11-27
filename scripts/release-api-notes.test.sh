#!/usr/bin/env bash
# Self-test of release-api-notes.sh: a break heads "Breaking API changes",
# a non-breaking difference heads "API changes (non-breaking)", no change
# prints nothing, and an unreadable or empty result fails.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
s="$here/release-api-notes.sh"
t=$(mktemp -d); trap 'rm -rf "$t"' EXIT
fail=0
check() { # name want-substring got
  if [[ $3 == *"$2"* ]]; then echo "ok   $1"; else echo "FAIL $1: want $2, got: $3"; fail=1; fi
}
printf '1 changes: 0 error, 0 warning, 1 info\ninfo\t[new-optional-request-parameter]\n' > "$t/cl.txt"
echo '[]' > "$t/none.json"
echo '[{"id":"request-parameter-removed","level":2}]' > "$t/one.json"
echo 'null' > "$t/null.json"
echo '{"x":1}' > "$t/obj.json"
out=$(bash "$s" "$t/one.json" "$t/cl.txt" v1.0.0); check "break heads Breaking" "## ⚠ Breaking API changes" "$out"
out=$(bash "$s" "$t/none.json" "$t/cl.txt" v1.0.0); check "no break heads non-breaking" "## API changes (non-breaking)" "$out"
if [[ $out != *Breaking* ]]; then echo "ok   no break never says Breaking"; else echo "FAIL no break says Breaking"; fail=1; fi
out=$(bash "$s" "$t/null.json" "$t/cl.txt" v1.0.0); check "null is no break" "(non-breaking)" "$out"
: > "$t/empty.txt"
out=$(bash "$s" "$t/none.json" "$t/empty.txt" v1.0.0); if [[ -z $out ]]; then echo "ok   no change prints nothing"; else echo "FAIL no change printed: $out"; fail=1; fi
if bash "$s" "$t/obj.json" "$t/cl.txt" v1.0.0 >/dev/null 2>&1; then echo "FAIL an object passed"; fail=1; else echo "ok   an object fails"; fi
: > "$t/empty.json"
if bash "$s" "$t/empty.json" "$t/cl.txt" v1.0.0 >/dev/null 2>&1; then echo "FAIL an empty file passed"; fail=1; else echo "ok   an empty file fails"; fi
if bash "$s" "$t/missing.json" "$t/cl.txt" v1.0.0 >/dev/null 2>&1; then echo "FAIL a missing file passed"; fail=1; else echo "ok   a missing file fails"; fi
exit $fail
