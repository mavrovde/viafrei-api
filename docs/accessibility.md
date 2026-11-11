# Accessibility of the documentation portal

The documentation portal is part of the product and must be usable by
everyone: WCAG 2.2 level AA (NFR-A11Y-001, NFR-A11Y-002, QAS-INT-02).
Automated checks run on every pull request; a person checks what tools
cannot, once per minor release.

## Automated, on every pull request

The `Documentation portal` CI job builds the portal and checks it.
`make docs` and `make docs-a11y` run the same checks locally.

| Check | Script | Fails on |
|---|---|---|
| Internal links | `scripts/docs-check.pl`, part of `scripts/docs-build.sh` | a link or image to a file the portal does not serve, a fragment that names no id of its page, a link out of the site |
| Duplicate ids | the same | an id used twice on one page |
| Diagrams | the same | a Mermaid diagram not followed by a paragraph starting `Text description:`; an image without alt text; an inline SVG without a `<title>` |
| WCAG 2.2 AA | `scripts/docs-a11y.sh` (pa11y-ci with axe, headless Chrome) | any critical or serious axe finding on any page, including the API reference |

Each check prints the pages it read and fails closed: a site without pages,
a page the browser could not load, or a report it could not read is a
failure, never a pass. External links are not fetched. Self-tests prove each
check can fail: `scripts/docs-check.test.sh`, `scripts/docs-a11y.test.sh`.

Findings axe cannot decide by itself, such as the contrast of text over a
background it cannot compute, are printed as notes for the manual pass and
do not fail the build.

## Writing accessible pages

- **Every diagram has an equivalent text description.** Put a paragraph
  starting `Text description:` right after a Mermaid block, saying what the
  diagram says, not what it looks like. Give every image an alt text that
  carries its meaning.
- Use headings in order, without skipping a level.
- Link text says where the link goes; never "click here".
- Never carry meaning by colour alone.

## Manual pass, once per minor release

Done by a maintainer on the portal built from the release candidate
(`make docs`, then open `site/index.html`), in a current browser and one
screen reader (VoiceOver, NVDA or Orca). Record the result in section 6 of
the release's test completion report: date, tester, browser and screen
reader with versions, each item's result, and every finding with its issue.

**Keyboard only (no mouse)**

- [ ] Every page: `Tab` reaches "Skip to content" first, and it moves the
      focus to the main content.
- [ ] Every link, button and scrolling code block or table can be reached
      with `Tab`, in reading order, and the focus is always visible.
- [ ] Wide code blocks and tables scroll with the arrow keys once focused.
- [ ] The API reference: the operation list, each operation, the request and
      response samples and the "Copy" buttons work with the keyboard alone.
- [ ] Nothing traps the focus.

**Screen reader**

- [ ] Each page announces its title and its language (English).
- [ ] The heading list of each page reads as an outline of the page.
- [ ] The navigation is announced as "Documentation" navigation, and the
      current page as current.
- [ ] Tables are read with their header cells.
- [ ] Every diagram's text description is read, and says what the diagram
      says.
- [ ] The API reference: the operation and parameter names, the required
      markers and the response codes are read.

**Display**

- [ ] At 400 % zoom (320 CSS pixels wide) no page needs horizontal scrolling
      except inside code blocks and tables, and nothing is cut off.
- [ ] With text spacing increased (line height 1.5, letter spacing 0.12 em,
      word spacing 0.16 em) no text is cut off or overlaps.
- [ ] Every note of the automated check marked "review" (contrast it could
      not compute) is checked by eye or with a contrast tool.
