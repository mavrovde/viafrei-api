#!/usr/bin/env bash
# openapi-diff.test.sh: the self-test of the accepted-break record that
# scripts/openapi-diff.sh passes to oasdiff (api/oasdiff-accepted.txt,
# docs/versioning.md). In a throwaway repository it plants a document and a
# breaking change of it, then checks:
# - an unlisted break fails;
# - the break listed exactly as oasdiff states it passes;
# - a line that differs by one character accepts nothing;
# - a listed break does not let a second, unlisted one through;
# - a missing record is a refusal (exit 2), never a pass.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
pass=0 fail=0

doc() { # doc MIN WITH_NAME: a one-operation document
  local name="" req='"level"'
  [[ $2 == yes ]] && name=', "name": {"type": "string"}' req='"level", "name"'
  cat <<EOF
{"openapi": "3.1.0", "info": {"title": "t", "version": "1"},
 "paths": {"/v1/x": {"get": {"operationId": "getX", "responses": {"200": {"description": "ok",
   "content": {"application/json": {"schema": {"type": "object", "required": [$req],
     "properties": {"level": {"type": "integer", "minimum": $1}$name}}}}}}}}}}
EOF
}

repo=$tmp/repo
mkdir -p "$repo/scripts" "$repo/api"
cp "$here/openapi-diff.sh" "$repo/scripts/"
cp "$here/../api/oasdiff-levels.txt" "$repo/api/"
git -C "$repo" init -q
doc 1 yes >"$repo/api/openapi.json"
git -C "$repo" add api/openapi.json
git -C "$repo" -c user.name=t -c user.email=t@example.test commit -q -m base
base=$(git -C "$repo" rev-parse HEAD)

line="GET /v1/x the \`level\` response property's min was decreased from \`1.00\` to \`0.00\` for the response status \`200\`"

expect() { # expect NAME WANT_EXIT
  local status
  (cd "$repo" && scripts/openapi-diff.sh "$base" api/openapi.json) >"$tmp/out" 2>&1
  status=$?
  if [[ $status != "$2" ]]; then
    echo "not ok: $1 (exit $status, want $2)"; sed 's/^/    /' "$tmp/out"; fail=$((fail + 1))
  else
    echo "ok: $1"; pass=$((pass + 1))
  fi
}

doc 0 yes >"$repo/api/openapi.json"
: >"$repo/api/oasdiff-accepted.txt"
expect "an unlisted break fails" 1
printf '%s\n' "$line" >"$repo/api/oasdiff-accepted.txt"
expect "the break listed as oasdiff states it passes" 0
printf '%s\n' "${line/0.00/0.50}" >"$repo/api/oasdiff-accepted.txt"
expect "a line one character off accepts nothing" 1
printf '%s\n' "$line" >"$repo/api/oasdiff-accepted.txt"
doc 0 no >"$repo/api/openapi.json"
expect "a listed break lets no second one through" 1
rm "$repo/api/oasdiff-accepted.txt"
expect "a missing record is a refusal" 2

echo "$pass passed, $fail failed"
[[ $fail == 0 ]]
