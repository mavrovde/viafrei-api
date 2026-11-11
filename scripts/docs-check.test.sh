#!/usr/bin/env bash
# docs-check.test.sh: the self-test of scripts/docs-check.pl, on planted
# sites. Each planted fault must fail the check, the clean site must pass,
# and a site without pages must be refused rather than reported clean.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
pass=0 fail=0

page() { # page FILE BODY
  printf '<!doctype html><html lang="en"><head><title>t</title></head><body><main id="main">%s</main></body></html>\n' "$2" > "$1"
}

clean() { # clean DIR: a site every check accepts
  rm -rf "$1" && mkdir -p "$1"
  page "$1/index.html" '<h1 id="top">Top</h1><a href="other.html#part">o</a> <a href="#top">t</a> <a href="https://example.org/x">x</a> <a href="openapi.json">d</a>
<pre tabindex="0"><code class="language-mermaid">flowchart TD
  A --&gt; B
</code></pre>
<p>Text description: A leads to B.</p>
<img src="a.png" alt="A diagram of A and B">'
  page "$1/other.html" '<h2 id="part">Part</h2><svg><title>A described drawing</title></svg>'
  echo '{}' > "$1/openapi.json"
  : > "$1/a.png"
}

expect() { # expect WANT_EXIT NAME DIR [WANT_TEXT]
  local out status
  out=$(perl "$here/docs-check.pl" "$3" 2>&1)
  status=$?
  if [[ $status != "$1" ]] || { [[ -n ${4:-} ]] && ! grep -qF -- "$4" <<<"$out"; }; then
    echo "not ok: $2 (exit $status)"; while IFS= read -r l; do echo "    $l"; done <<<"$out"; fail=$((fail + 1))
  else
    echo "ok: $2"; pass=$((pass + 1))
  fi
}

s=$tmp/site
clean "$s"; expect 0 "a clean site passes" "$s" "2 page(s)"
clean "$s"; expect 0 "the clean run names every page it read" "$s" "docs check: read other.html"

clean "$s"; sed -i.bak 's/other.html#part/missing.html/' "$s/index.html"; expect 1 "a link to a missing page fails" "$s" 'names no file of the site'
clean "$s"; sed -i.bak 's/other.html#part/other.html#nowhere/' "$s/index.html"; expect 1 "a link to a missing fragment fails" "$s" 'names no id of other.html'
clean "$s"; sed -i.bak 's/href="#top"/href="#gone"/' "$s/index.html"; expect 1 "a fragment-only link to a missing id fails" "$s" 'names no id of index.html'
clean "$s"; sed -i.bak 's#other.html\#part#../outside.html#' "$s/index.html"; expect 1 "a link out of the site fails" "$s" 'leaves the site'
clean "$s"; sed -i.bak 's/id="part"/id="top"/' "$s/other.html"; sed -i.bak 's#<h2 id="top">Part</h2>#&<h3 id="top">Again</h3>#' "$s/other.html"; expect 1 "a duplicate id fails" "$s" 'appears 2 times'

clean "$s"; sed -i.bak 's/Text description: A leads to B./A leads to B./' "$s/index.html"; expect 1 "a diagram without a text description fails" "$s" 'has no text description'
clean "$s"; sed -i.bak 's/ alt="A diagram of A and B"//' "$s/index.html"; expect 1 "an image without alt fails" "$s" 'image without alt text'
clean "$s"; sed -i.bak 's/ alt="A diagram of A and B"/ alt=" "/' "$s/index.html"; expect 1 "an image with a blank alt fails" "$s" 'image without alt text'
clean "$s"; sed -i.bak 's#<title>A described drawing</title>##' "$s/other.html"; expect 1 "an inline SVG without a title fails" "$s" 'inline SVG without a <title>'

clean "$s"; cp "$s/other.html" "$s/reference.html"; sed -i.bak 's#<title>A described drawing</title>##' "$s/reference.html"; expect 0 "the reference's icons are not diagrams" "$s"
clean "$s"; sed -i.bak 's/#part/#nowhere/' "$s/other.html"; cp "$s/index.html" "$s/reference.html"; sed -i.bak 's/#top/#gone/' "$s/reference.html"; expect 1 "the reference's links are checked" "$s" 'reference.html: link'

# A link written in a Markdown source, through the build's link rewrite
# (docs-html.pl, from docs/): a misspelt page or a missing script must reach
# the site as a link the check refuses; an existing script becomes GitHub's.
rewrite() { # rewrite HREF: the href docs-html.pl makes of it, from docs/
  (cd "$tmp" && DOCS_PAGES="docs/errors.md=errors.html" DOCS_SITE_FILES="" \
    perl "$here/docs-html.pl" docs <<<"<a href=\"$1\">x</a>" | sed -n 's/.*href="\([^"]*\)".*/\1/p')
}
clean "$s"; sed -i.bak "s#other.html\#part#$(rewrite eror.md)#" "$s/index.html"; expect 1 "a misspelt page link (docs/eror.md) fails" "$s" 'missing-in-repo/docs/eror.md'
clean "$s"; sed -i.bak "s#other.html\#part#$(rewrite ../scripts/nope.sh)#" "$s/index.html"; expect 1 "a link to a missing script (../scripts/nope.sh) fails" "$s" 'missing-in-repo/scripts/nope.sh'
clean "$s"; sed -i.bak "s#other.html\#part#$(rewrite ../scripts/docs-check.pl)#" "$s/index.html"; expect 0 "a link to an existing script goes to GitHub" "$s"
if [[ $(rewrite ../scripts/docs-check.pl) == https://github.com/*/blob/main/scripts/docs-check.pl ]]; then
  echo "ok: the GitHub link names the file"; pass=$((pass + 1))
else
  echo "not ok: the GitHub link names the file: $(rewrite ../scripts/docs-check.pl)"; fail=$((fail + 1))
fi

rm -rf "$s" && mkdir -p "$s"; expect 2 "a site without pages is refused" "$s" 'no page found'
expect 2 "a missing site is refused" "$tmp/none" 'cannot read'

echo "docs-check self-test: $pass passed, $fail failed"
(( fail == 0 ))
