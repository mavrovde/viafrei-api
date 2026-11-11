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
# with a missing page is not published.
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
  body=$("${marked[@]}" -i "$src")
  [[ -n $body ]] || { echo "FAIL: $src rendered to nothing" >&2; exit 1; }
  body=$(perl "$here/docs-html.pl" "$base" <<<"$body")
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
table{border-collapse:collapse;display:block;overflow-x:auto}th,td{border:1px solid #767676;padding:.3rem .5rem;text-align:left}
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
"${redocly[@]}" build-docs api/openapi.json --output "$out/reference.html" >/dev/null
title=$(jq -r '.info.title' api/openapi.json)
grep -qF "$title" "$out/reference.html" || { echo "FAIL: reference.html does not carry the document title '$title'" >&2; exit 1; }
touch "$out/.nojekyll"

for f in "$out"/*.html "$out/openapi.json"; do
  [[ -s $f ]] || { echo "FAIL: $f is empty" >&2; exit 1; }
done
echo "built $(find "$out" -type f | wc -l | tr -d ' ') file(s) into $out for version $version"
