#!/usr/bin/env bash
# docs-a11y.test.sh: the self-test of scripts/docs-a11y.sh. A site without
# pages is refused before any browser starts; a planted critical finding (an
# image without a text alternative) fails; a clean page passes and is named.
# Needs the Chrome docs-a11y.sh finds (CHROME_PATH or on PATH).
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
pass=0 fail=0

expect() { # expect WANT_EXIT NAME DIR WANT_TEXT
  local out status
  out=$("$here/docs-a11y.sh" "$3" 2>&1)
  status=$?
  if [[ $status != "$1" ]] || ! grep -qF -- "$4" <<<"$out"; then
    echo "not ok: $2 (exit $status, want $1)"; while IFS= read -r l; do echo "    $l"; done <<<"$out"; fail=$((fail + 1))
  else
    echo "ok: $2"; pass=$((pass + 1))
  fi
}

page() { # page FILE BODY
  printf '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Planted page</title></head><body><main><h1>Planted page</h1>%s</main></body></html>\n' "$2" > "$1"
}

mkdir -p "$tmp/empty"
expect 2 "a site without pages is refused" "$tmp/empty" "no page found"

mkdir -p "$tmp/clean"
page "$tmp/clean/index.html" '<p>Nothing to find here.</p>'
expect 0 "a clean page passes and is named" "$tmp/clean" "docs a11y: read index.html"

mkdir -p "$tmp/broken"
page "$tmp/broken/index.html" '<p>Nothing to find here.</p>'
page "$tmp/broken/diagram.html" '<img src="diagram.png">'
expect 1 "an image without alt fails" "$tmp/broken" "FAIL: diagram.html: image-alt"

echo "docs-a11y self-test: $pass passed, $fail failed"
(( fail == 0 ))
