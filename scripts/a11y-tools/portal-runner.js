'use strict';
// A pa11y runner for scripts/docs-a11y.sh beside axe-runner.js and
// layout-runner.js: the items of the manual pass (docs/accessibility.md)
// that a script can hold on every page, so a finding of a pass (#183)
// cannot come back unseen. Each failure is an "error" issue, which
// docs-a11y.sh fails on every page, reference.html included.
//
// The keyboard reaches an element that is rendered, visible, not disabled
// and not taken out of the tab order (tabindex -1).
//
// - skip-link: the first element the keyboard reaches is a "Skip to
//   content" link to the main landmark or an element in it.
// - focus-visible: every kind of focusable element (its tag and first
//   class; one of each, since every focus restyles the page) shows an
//   outline of at least 2 px or a box shadow while it has the focus.
// - heading-name: a heading that holds a labelled element (Redoc's
//   anchor link) is named by its own text, not by the label.
// - table-headers: every rendered table has a header cell.
// - clipped-text: no rendered element with text hides part of it (a
//   horizontal or vertical overflow that is hidden or clipped), at the
//   page's own text spacing and at the spacing WCAG 1.4.12 asks to bear;
//   an element at most 1 px wide is visually hidden text, not clipped.
// - reflow: in a viewport at most 320 px wide (400 % zoom), the page does
//   not scroll sideways (WCAG 1.4.10) and no text is cut off; docs-a11y.sh
//   loads every page once more at that width. The text spacing is checked
//   at the page's own width, as the manual pass checks it.
//
// The run function is stringified by pa11y and evaluated in the page, so it
// may use nothing from this module's scope. scripts/a11y-tools/
// portal-runner.test.js runs it under node:test against a fake window.

const run = async () => {
  const doc = window.document;
  const issues = [];
  const fail = (code, element, message) => issues.push({code, type: 'error', element, message, runnerExtras: {}});
  const rendered = e => e.getClientRects().length > 0;
  const words = s => s.split(/\s+/).filter(Boolean).join(' ');

  const focusable = [...doc.querySelectorAll('a[href], button, input, select, textarea, [tabindex]')]
    .filter(e => e.tabIndex >= 0 && !e.disabled && rendered(e) && window.getComputedStyle(e).visibility !== 'hidden');

  const first = focusable[0];
  const href = first?.getAttribute('href') || '';
  const target = href.startsWith('#') ? doc.getElementById(href.slice(1)) : null;
  const main = target?.closest('main, [role=main]');
  if (!first || first.tagName !== 'A' || words(first.textContent) !== 'Skip to content' || !main) {
    fail('skip-link', first || null, 'the first element the keyboard reaches is not a "Skip to content" link to the main landmark');
  }

  const kinds = new Map();
  for (const e of focusable) {
    const kind = `${e.tagName} ${String(e.className).split(' ')[0]}`;
    if (!kinds.has(kind)) kinds.set(kind, e);
  }
  // Focusing must not scroll: Redoc's scroll spy would rewrite the URL's
  // hash, and pa11y reports a page under the URL it ends on, which
  // docs-a11y.sh would then miss. The URL is put back if anything moved it.
  const before = doc.activeElement;
  const url = window.location.href;
  for (const [kind, e] of kinds) {
    e.focus({preventScroll: true});
    const st = window.getComputedStyle(e);
    const outline = st.outlineStyle !== 'none' && Number.parseFloat(st.outlineWidth) >= 2;
    if (doc.activeElement !== e || (!outline && st.boxShadow === 'none')) {
      fail('focus-visible', e, `${kind} shows no outline of 2 px or more and no box shadow while it has the focus`);
    }
  }
  if (before?.focus) before.focus({preventScroll: true});
  if (window.location.href !== url) window.history.replaceState(window.history.state, '', url);

  for (const h of doc.querySelectorAll('h1, h2, h3, h4, h5, h6')) {
    if (h.querySelector('[aria-label]') && h.getAttribute('aria-label') !== words(h.textContent)) {
      fail('heading-name', h, `the heading "${words(h.textContent)}" is named by the label of an element in it, not by its own text`);
    }
  }

  for (const t of doc.querySelectorAll('table')) {
    if (rendered(t) && !t.querySelector('th, [role=columnheader], [role=rowheader]')) {
      fail('table-headers', t, 'a table has no header cell');
    }
  }

  const clipped = spacing => {
    for (const e of doc.body.querySelectorAll('*')) {
      if (!e.textContent.trim() || e.clientWidth <= 1 || !rendered(e)) continue;
      const st = window.getComputedStyle(e);
      const hides = v => v === 'hidden' || v === 'clip';
      if ((hides(st.overflowX) && e.scrollWidth > e.clientWidth + 1) || (hides(st.overflowY) && e.scrollHeight > e.clientHeight + 1)) {
        fail('clipped-text', e, `text is cut off ${spacing}: "${words(e.textContent).slice(0, 60)}"`);
      }
    }
  };
  clipped('at the page\'s text spacing');
  if (window.innerWidth <= 320) {
    const width = doc.documentElement.clientWidth;
    if (doc.scrollingElement.scrollWidth > width + 1) {
      fail('reflow', null, `at ${width} px wide the page scrolls sideways (${doc.scrollingElement.scrollWidth} px wide)`);
    }
    return issues;
  }
  const spaced = doc.createElement('style');
  spaced.textContent = '*{line-height:1.5!important;letter-spacing:0.12em!important;word-spacing:0.16em!important}p{margin-bottom:2em!important}';
  doc.head.appendChild(spaced);
  try {
    clipped('at the WCAG 1.4.12 text spacing');
  } finally {
    spaced.remove();
  }
  return issues;
};

module.exports = {run, scripts: [], supports: '^10.0.0'};
