#!/usr/bin/env bash
# docs-offsite.sh SITE_DIR
#
# The privacy check of the built documentation portal (#81): every page,
# the Redoc API reference included, is opened in headless Chrome
# (scripts/a11y-tools/offsite.js) and must make no request to any host but
# the site's own. A web font, stylesheet, script or image from a third party
# hands that party the visitor's address before any consent. Off-site
# requests are aborted, so the check contacts no host itself.
#
# The static half of the same rule is scripts/docs-check.pl, which fails the
# docs build on a page that names an external resource; this half catches
# what a page's script requests at run time.
#
# Fails closed: no page, or a page that did not load, is a refusal (exit 2).
# The toolchain is the pinned one of scripts/a11y-tools (npm ci
# --ignore-scripts); Chrome is the system's: CHROME_PATH, else the first of
# google-chrome, chromium, chromium-browser on PATH, else the macOS
# application.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
site=${1:?usage: docs-offsite.sh SITE_DIR}
[[ -d $site ]] || { echo "FAIL: cannot read $site" >&2; exit 2; }
site=$(cd "$site" && pwd)
pages=()
while IFS= read -r f; do pages+=("${f#"$site"/}"); done < <(find "$site" -maxdepth 1 -name '*.html' -type f | sort)
(( ${#pages[@]} > 0 )) || { echo "FAIL: no page found in $site: refusing to report a clean site" >&2; exit 2; }

tools=$here/a11y-tools
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

for p in "${pages[@]}"; do echo "docs offsite: found $p"; done
exec node "$tools/offsite.js" "$chrome" "$site" "${pages[@]}"
