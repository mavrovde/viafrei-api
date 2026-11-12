#!/usr/bin/env bash
# docs-offsite.test.sh [LCOV]
#
# The self-test of the portal's privacy check (#81):
#
# - scripts/a11y-tools/offsite.test.js under node:test, held to 100 % line,
#   branch and function coverage (a lower figure fails); LCOV (default
#   coverage-offsite.lcov) receives the report SonarCloud reads;
# - scripts/docs-offsite.sh on planted sites in the real Chrome: a site
#   without pages is refused, a page with only its own files passes, and a
#   page that loads a Google web font, a font file through @font-face, a
#   third-party script or an image from another host each fail.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
lcov=${1:-coverage-offsite.lcov}
[[ $lcov == /* ]] || lcov=$PWD/$lcov
tools=$here/a11y-tools
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
pass=0 fail=0

npm ci --prefix "$tools" --ignore-scripts --no-audit --no-fund --loglevel=error >/dev/null ||
  { echo "FAIL: could not install the pinned toolchain in $tools" >&2; exit 2; }
chrome=${CHROME_PATH:-}
if [[ -z $chrome ]]; then
  for c in google-chrome chromium chromium-browser; do
    chrome=$(command -v "$c" || true)
    [[ -n $chrome ]] && break
  done
fi
[[ -n $chrome ]] || chrome="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
[[ -x $chrome ]] || { echo "FAIL: no Chrome found; set CHROME_PATH" >&2; exit 2; }
export CHROME_PATH=$chrome

if (cd "$root" && node --test --experimental-test-coverage \
  --test-coverage-include=scripts/a11y-tools/offsite.js \
  --test-coverage-lines=100 --test-coverage-branches=100 --test-coverage-functions=100 \
  --test-reporter=spec --test-reporter-destination=stdout \
  --test-reporter=lcov --test-reporter-destination="$lcov" \
  scripts/a11y-tools/offsite.test.js) &&
  grep -q '^SF:.*scripts/a11y-tools/offsite\.js$' "$lcov" 2>/dev/null; then
  echo "ok: offsite.js unit test at 100 % coverage, report in $lcov"; pass=$((pass + 1))
else
  echo "not ok: offsite.js unit test at 100 % coverage"; fail=$((fail + 1))
fi

expect() { # expect WANT_EXIT NAME DIR WANT_TEXT
  local out status
  out=$("$here/docs-offsite.sh" "$3" 2>&1)
  status=$?
  if [[ $status != "$1" ]] || ! grep -qF -- "$4" <<<"$out"; then
    echo "not ok: $2 (exit $status, want $1)"; while IFS= read -r l; do echo "    $l"; done <<<"$out"; fail=$((fail + 1))
  else
    echo "ok: $2"; pass=$((pass + 1))
  fi
}

page() { # page FILE HEAD BODY
  printf '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Planted</title>%s</head><body><main><h1>Planted</h1><p>Text.</p>%s</main></body></html>\n' "$2" "$3" > "$1"
}

mkdir -p "$tmp/empty"
expect 2 "a site without pages is refused" "$tmp/empty" "no page found"
expect 2 "a missing site is refused" "$tmp/none" "cannot read"

mkdir -p "$tmp/clean"
page "$tmp/clean/index.html" '<link rel="stylesheet" href="own.css">' '<a href="https://example.org/">a link is not a request</a>'
echo 'p{font-family:system-ui,sans-serif}' > "$tmp/clean/own.css"
expect 0 "a page with its own files only passes and is named" "$tmp/clean" "docs offsite: read index.html"

plant() { # plant NAME HEAD BODY: a site of the clean page plus one leaking page
  rm -rf "${tmp:?}/${1:?}" && mkdir -p "$tmp/$1" && cp "$tmp/clean/"* "$tmp/$1/"
  page "$tmp/$1/leak.html" "$2" "$3"
}
plant gfont '<link href="https://fonts.googleapis.com/css?family=Montserrat:300,400,700|Roboto:300,400,700" rel="stylesheet">' ''
expect 1 "a Google web font stylesheet fails" "$tmp/gfont" "FAIL: leak.html: requests https://fonts.googleapis.com/"
plant fontface '<style>@font-face{font-family:X;src:url(https://fonts.gstatic.com/s/x.woff2)}p{font-family:X}</style>' ''
expect 1 "a font file from another host through @font-face fails" "$tmp/fontface" "FAIL: leak.html: requests https://fonts.gstatic.com/"
plant script '' '<script src="https://cdn.example/redoc.standalone.js"></script>'
expect 1 "a third-party script fails" "$tmp/script" "FAIL: leak.html: requests https://cdn.example/"
plant image '' '<img alt="x" src="https://192.0.2.1/pixel.png">'
expect 1 "an image from another host fails" "$tmp/image" "FAIL: leak.html: requests https://192.0.2.1/"
plant runtime '' '<script>var l=document.createElement("link");l.rel="stylesheet";l.href="https://fonts.googleapis.com/css?family=Late";document.head.appendChild(l)</script>'
expect 1 "a stylesheet a script adds at run time fails" "$tmp/runtime" "FAIL: leak.html: requests https://fonts.googleapis.com/css?family=Late"

echo "docs-offsite self-test: $pass passed, $fail failed"
(( fail == 0 && pass >= 9 ))
