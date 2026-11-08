#!/usr/bin/env bash
# docs-build.sh OUT_DIR [VERSION]
#
# Builds the documentation site that release.yml publishes to GitHub Pages:
# the OpenAPI reference (reference.html, rendered by Redocly from
# api/openapi.json, and the document itself as openapi.json) and the guide
# (index.html from README.md, one page per guide file under docs/). CI runs
# it on every pull request, so a release never meets a site that does not
# build. Each tool is pinned; nothing is installed into the repository.
#
# Fails when a page came out empty or the reference does not carry the
# document's title: a site with a missing page is not published.
set -euo pipefail

out=${1:?usage: docs-build.sh OUT_DIR [VERSION]}
version=${2:-$(tr -d '[:space:]' < version.txt)}
marked=(npm exec --yes --ignore-scripts --package=marked@18.1.1 -- marked --gfm)
redocly=(npm exec --yes --ignore-scripts --package=@redocly/cli@2.64.1 -- redocly)

# The guide: source file, page, title.
pages=(
  "README.md|index.html|ViaFrei API"
  "docs/configuration.md|configuration.html|Configuration reference"
  "docs/container.md|container.html|Container image and local stack"
  "docs/security-checks.md|security-checks.html|Security checks"
)

rm -rf "$out"
mkdir -p "$out"

nav='<nav><a href="index.html">Guide</a> · <a href="reference.html">API reference</a> · <a href="configuration.html">Configuration</a> · <a href="container.html">Container</a> · <a href="security-checks.html">Security checks</a> · <a href="openapi.json">openapi.json</a></nav>'

for p in "${pages[@]}"; do
  IFS='|' read -r src page title <<<"$p"
  [[ -s $src ]] || { echo "FAIL: $src is missing or empty" >&2; exit 1; }
  body=$("${marked[@]}" -i "$src")
  [[ -n $body ]] || { echo "FAIL: $src rendered to nothing" >&2; exit 1; }
  # Links between guide files point at their pages on this site.
  body=$(sed -E 's#href="(docs/)?(configuration|container|security-checks)\.md#href="\2.html#g' <<<"$body")
  cat > "$out/$page" <<EOF
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>$title · ViaFrei API $version</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:60rem;margin:0 auto;padding:1rem}table{border-collapse:collapse;display:block;overflow-x:auto}th,td{border:1px solid #ccc;padding:.3rem .5rem;text-align:left}code,pre{background:#f4f4f4}pre{padding:.5rem;overflow-x:auto}nav{margin-bottom:1rem}</style>
</head>
<body>
$nav
<p><small>Version $version</small></p>
$body
</body>
</html>
EOF
done

cp api/openapi.json "$out/openapi.json"
"${redocly[@]}" build-docs api/openapi.json --output "$out/reference.html" >/dev/null
title=$(jq -r '.info.title' api/openapi.json)
grep -qF "$title" "$out/reference.html" || { echo "FAIL: reference.html does not carry the document title '$title'" >&2; exit 1; }
touch "$out/.nojekyll"

for f in "$out"/*.html "$out/openapi.json"; do
  [[ -s $f ]] || { echo "FAIL: $f is empty" >&2; exit 1; }
done
echo "built $(find "$out" -type f | wc -l | tr -d ' ') file(s) into $out for version $version"
