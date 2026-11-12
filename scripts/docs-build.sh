#!/usr/bin/env bash
# docs-build.sh OUT_DIR [VERSION]
#
# Builds the documentation portal that release.yml publishes to GitHub Pages:
# the guide (index.html from README.md, one page per guide file under docs/),
# the endpoints page (endpoints.html, generated from api/openapi.json by
# scripts/docs-endpoints.jq: every operation with its description, request,
# recorded answer and attribution rule), the changelog, the OpenAPI reference
# (reference.html, rendered by Redocly) and the document itself
# (openapi.json). CI runs it on every pull request, so a release never meets
# a site that does not build. Each tool is pinned; nothing is installed into
# the repository.
#
# Links between guide files point at their pages; a link to any other file
# of the repository points at it on GitHub (scripts/docs-html.pl).
#
# Fails when a page came out empty, the endpoints page misses an operation of
# the document, or the reference does not carry the document's title: a site
# with a missing page is not published. Fails as well on a broken internal
# link, a duplicate id or a diagram without a text description
# (scripts/docs-check.pl).
set -euo pipefail

out=${1:?usage: docs-build.sh OUT_DIR [VERSION]}
version=${2:-$(tr -d '[:space:]' < version.txt)}
marked=(npm exec --yes --ignore-scripts --package=marked@18.1.1 -- marked --gfm)
redocly=(npm exec --yes --ignore-scripts --package=@redocly/cli@2.64.1 -- redocly)
here=$(cd "$(dirname "$0")" && pwd)

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
jq -r -f "$here/docs-endpoints.jq" api/openapi.json > "$tmp/endpoints.md"

# The portal: source file, the directory its links resolve against, page,
# title, navigation label (empty: not in the navigation bar).
pages=(
  "README.md|.|index.html|ViaFrei API|Overview"
  "docs/getting-started.md|docs|getting-started.html|Getting started|Getting started"
  "$tmp/endpoints.md|docs|endpoints.html|Endpoints|Endpoints"
  "docs/attribution.md|docs|attribution.html|Attribution and licence rules|Attribution"
  "docs/errors.md|docs|errors.html|Error codes|Errors"
  "docs/versioning.md|docs|versioning.html|Versioning and deprecation policy|Versioning"
  "docs/rate-limits.md|docs|rate-limits.html|Rate limits, quotas and priority|Rate limits"
  "docs/access-keys.md|docs|access-keys.html|API keys and the licence gate|Keys"
  "docs/examples.md|docs|examples.html|Examples|"
  "docs/accessibility.md|docs|accessibility.html|Accessibility of the documentation portal|"
  "docs/configuration.md|docs|configuration.html|Configuration reference|"
  "docs/container.md|docs|container.html|Container image and local stack|"
  "docs/security-checks.md|docs|security-checks.html|Security checks|"
  "CHANGELOG.md|.|changelog.html|Changelog|Changelog"
)

rm -rf "$out"
mkdir -p "$out"

# Which repository file each page stands for, and the files the site serves.
DOCS_PAGES="docs/endpoints.md=endpoints.html"
DOCS_SITE_FILES="reference.html openapi.json"
for p in "${pages[@]}"; do
  IFS='|' read -r src _ page _ _ <<<"$p"
  [[ $src == "$tmp/"* ]] || DOCS_PAGES+=" $src=$page"
  DOCS_SITE_FILES+=" $page"
done
export DOCS_PAGES DOCS_SITE_FILES

for p in "${pages[@]}"; do
  IFS='|' read -r src base page title _ <<<"$p"
  [[ -s $src ]] || { echo "FAIL: $src is missing or empty" >&2; exit 1; }
  # A line that is only an image from another host (the README's status
  # badges) is GitHub's decoration and is left out of the portal, whose
  # pages load nothing from a third party (#81, scripts/docs-check.pl). A
  # line inside a fenced code block is text and is kept.
  body=$(awk '/^[[:space:]]*(```|~~~)/ { fenced = !fenced; print; next }
    fenced || !/^[[:space:]]*!\[[^]]*\]\((https?:)?\/\/[^)]*\)[[:space:]]*$/' "$src" | "${marked[@]}")
  [[ -n $body ]] || { echo "FAIL: $src rendered to nothing" >&2; exit 1; }
  body=$(perl "$here/docs-html.pl" "$base" <<<"$body") || { echo "FAIL: $src has the link(s) above" >&2; exit 1; }
  nav=''
  for q in "${pages[@]}" "|.|reference.html||API reference" "|.|openapi.json||openapi.json"; do
    IFS='|' read -r _ _ target _ label <<<"$q"
    [[ -n $label ]] || continue
    current=''
    [[ $target == "$page" ]] && current=' aria-current="page"'
    nav+="<li><a href=\"$target\"$current>$label</a></li>"
  done
  cat > "$out/$page" <<EOF
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>$title · ViaFrei API $version</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;max-width:60rem;margin:0 auto;padding:1rem;color:#1a1a1a;background:#fff}
a{color:#0b57d0}a:focus-visible{outline:3px solid #0b57d0;outline-offset:2px}
.skip{position:absolute;left:-999px}.skip:focus{left:1rem;top:1rem;background:#fff;padding:.5rem}
nav ul{list-style:none;padding:0;margin:0 0 1rem;display:flex;flex-wrap:wrap;gap:.25rem 1rem}
nav a[aria-current]{font-weight:700}
.table-wrap{overflow-x:auto}table{border-collapse:collapse}th,td{border:1px solid #767676;padding:.3rem .5rem;text-align:left;overflow-wrap:anywhere}
code,pre{background:#f4f4f4}pre{padding:.5rem;overflow-x:auto}
</style>
</head>
<body>
<a class="skip" href="#main">Skip to content</a>
<header>
<nav aria-label="Documentation"><ul>$nav</ul></nav>
<p><small>Version $version</small></p>
</header>
<main id="main">
$body
</main>
</body>
</html>
EOF
done

# Every operation of the document has its section on the endpoints page.
while read -r id; do
  grep -qF "id=\"$(tr '[:upper:]' '[:lower:]' <<<"$id")\"" "$out/endpoints.html" ||
    { echo "FAIL: endpoints.html has no section for $id" >&2; exit 1; }
done < <(jq -r '.paths[][] | objects | .operationId // empty' api/openapi.json)

cp api/openapi.json "$out/openapi.json"
# No request may leave the site when a page is opened (#81): Redoc's Google
# web fonts are switched off and its theme uses the system font stack, like
# the guide pages; its script, which the CLI links from Redocly's CDN, is
# served from the site itself (below).
fonts='system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif'
"${redocly[@]}" build-docs api/openapi.json --disableGoogleFont \
  --theme.openapi.jsonSamplesExpandLevel=all \
  --theme.openapi.theme.typography.fontFamily="$fonts" \
  --theme.openapi.theme.typography.headings.fontFamily="$fonts" \
  --output "$out/reference.html" >/dev/null
# The script tag the CLI wrote names the Redoc version and the bundle's
# sha384. The same version is fetched from the npm registry, must have that
# very digest (so it is the file the CDN would have served), and is served
# beside the page with its licence files; the tag then names the local copy.
cdn='<script src="https://cdn\.redocly\.com/redoc/v([0-9][0-9.]*)/bundles/redoc\.standalone\.js" integrity="sha384-([A-Za-z0-9+/=]+)" crossorigin="anonymous"></script>'
[[ $(grep -cE "$cdn" "$out/reference.html") == 1 ]] ||
  { echo "FAIL: reference.html does not carry exactly one Redoc CDN script tag to replace; the CLI's template changed" >&2; exit 1; }
redoc_version=$(grep -oE "$cdn" "$out/reference.html" | sed -E "s#$cdn#\\1#")
redoc_digest=$(grep -oE "$cdn" "$out/reference.html" | sed -E "s#$cdn#\\2#")
npm pack --silent --pack-destination "$tmp" "redoc@$redoc_version" >/dev/null ||
  { echo "FAIL: could not fetch redoc@$redoc_version from the npm registry" >&2; exit 1; }
tar -xzf "$tmp/redoc-$redoc_version.tgz" -C "$tmp"
got=$(openssl dgst -sha384 -binary "$tmp/package/bundles/redoc.standalone.js" | openssl base64 -A)
[[ $got == "$redoc_digest" ]] ||
  { echo "FAIL: redoc@$redoc_version's redoc.standalone.js is not the bundle the CLI names (sha384 differs)" >&2; exit 1; }
cp "$tmp/package/bundles/redoc.standalone.js" "$tmp/package/bundles/redoc.standalone.js.LICENSE.txt" "$out/"
cp "$tmp/package/LICENSE" "$out/redoc.LICENSE.txt"
# The one change to the bundle (MIT, redoc.LICENSE.txt): its sidebar credit
# loads Redocly's logo from Redocly's CDN at run time. The image source
# becomes an empty data: URL, which fails to decode, so the bundle's own
# error handler drops the image and the credit's text stays; no request
# leaves the site (scripts/docs-offsite.sh).
logo='src:"https://cdn.redoc.ly/redoc/logo-mini.svg"'
[[ $(grep -oF "$logo" "$out/redoc.standalone.js" | wc -l | tr -d ' ') == 1 ]] ||
  { echo "FAIL: redoc.standalone.js does not load the sidebar logo from the CDN exactly once; the bundle changed" >&2; exit 1; }
perl -pi -e 's{\Qsrc:"https://cdn.redoc.ly/redoc/logo-mini.svg"\E}{src:"data:,"}' "$out/redoc.standalone.js"
perl -0pi -e 's{<script src="https://cdn\.redocly\.com/redoc/v[0-9.]+/bundles/redoc\.standalone\.js"[^>]*></script>}{<script src="redoc.standalone.js"></script>}' "$out/reference.html"
title=$(jq -r '.info.title' api/openapi.json)
grep -qF "$title" "$out/reference.html" || { echo "FAIL: reference.html does not carry the document title '$title'" >&2; exit 1; }
# WCAG 2.2 AA repairs Redoc's theme cannot make (scripts/docs-a11y.sh;
# scripts/a11y-tools/layout-runner.js checks the last two in the rendered
# page):
# - its JSON expand buttons are 15 px, under the 24 px target size;
# - its code samples scroll without being reachable by keyboard;
# - its page sits in no landmark (axe: region, landmark-one-main): the
#   content column becomes the main landmark and the menu a navigation
#   landmark, in the prerendered markup and again whenever Redoc renders
#   them anew;
# - its schema tables draw the first and the last row's tree-line segment as
#   a 1 px background image on the property-name cell, which leaves the
#   name's contrast undecidable. The image goes; the same segment (from
#   22 px to the bottom on the first row, from the top to 22 px on the last,
#   Redoc's own geometry) is a ::before border whose colour is inherited
#   from the cell's own border-left-color, Redoc's theme line colour (and
#   transparent where Redoc hides the line), so no colour is copied here.
for c in menu-content api-content; do
  [[ $(grep -o "class=\"[^\"]* $c\"" "$out/reference.html" | wc -l | tr -d ' ') == 1 ]] ||
    { echo "FAIL: reference.html does not carry exactly one Redoc .$c to mark as a landmark; the bundle changed" >&2; exit 1; }
done
perl -0pi -e '
  s{(class="[^"]* menu-content")}{$1 role="navigation" aria-label="Reference contents"};
  s{(class="[^"]* api-content")}{$1 role="main"};
  s{</head>}{<style>.redoc-json .collapser{display:none!important}td[kind=field]{background-image:none!important}tr:first-of-type>td[kind=field]::before,tr.last>td[kind=field]::before{content:"";position:absolute;left:0;border-left:1px solid;border-left-color:inherit}tr:first-of-type>td[kind=field]::before{top:22px;bottom:0}tr.last>td[kind=field]::before{top:0;bottom:auto;height:22px}</style></head>};
  s{</body>}{<script>(function(){function f(){document.querySelectorAll("pre:not([tabindex])").forEach(function(p){p.setAttribute("tabindex","0")});document.querySelectorAll(".menu-content:not([role])").forEach(function(m){m.setAttribute("role","navigation");m.setAttribute("aria-label","Reference contents")});document.querySelectorAll(".api-content:not([role])").forEach(function(m){m.setAttribute("role","main")})}document.addEventListener("DOMContentLoaded",f);new MutationObserver(f).observe(document.documentElement,{childList:true,subtree:true})})();</script></body>};
' "$out/reference.html"
touch "$out/.nojekyll"

for f in "$out"/*.html "$out/openapi.json"; do
  [[ -s $f ]] || { echo "FAIL: $f is empty" >&2; exit 1; }
done
# Broken internal links, duplicate ids and diagrams without a text
# description fail the build (#20). Accessibility: scripts/docs-a11y.sh.
perl "$here/docs-check.pl" "$out"
echo "built $(find "$out" -type f | wc -l | tr -d ' ') file(s) into $out for version $version"
