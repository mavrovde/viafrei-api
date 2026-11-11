'use strict';
// A pa11y runner for scripts/docs-a11y.sh: axe-core, run as pa11y's own axe
// runner runs it (WCAG 2.0 and 2.1 A/AA tags and best practices, rules
// enabled or disabled by name), with additions in runnerExtras that the
// built-in runner drops:
//
// - target: axe's selector for the node, joined into one string (a node in a
//   frame or a shadow root has an array of selectors). docs-a11y.sh counts a
//   non-colour result by it, with nth-child parts and numbers removed.
// - for color-contrast, colourSource and the colour pair it is counted by:
//   - "axe": the text and background colour axe compared;
//   - "computed": axe could not compare them (a "needs review" result, such
//     as text partly obscured), so the element's computed text colour and the
//     background colour of the nearest element at or above it that paints
//     one, or "canvas" when none does;
//   - "undetermined": no pair can be read off the styles: the node has no
//     element, or the walk met a background image, an opacity below 1 or a
//     partly transparent background colour. fgColor and bgColor are null,
//     and undeterminedBy says which.
// - messageKey: why axe could not decide, when it says.
//
// The run function is stringified by pa11y and evaluated in the page, so it
// may use nothing from this module's scope. scripts/a11y-tools/
// axe-runner.test.js runs it under node:test against a fake window.
const path = require('node:path');

const axePath = path.dirname(require.resolve('axe-core'));

const run = async options => {
  if (options.standard !== 'WCAG2AA') {
    throw new Error(`docs a11y runner: standard ${options.standard} is not WCAG2AA`);
  }
  const rules = {};
  for (const id of options.rules || []) rules[id] = {enabled: true};
  for (const id of options.ignore || []) rules[id] = {enabled: false};
  const {violations = [], incomplete = []} = await window.axe.run(window.document, {
    runOnly: {type: 'tags', values: ['wcag2a', 'wcag21a', 'wcag2aa', 'wcag21aa', 'best-practice']},
    rules
  });
  const level = {critical: 'error', serious: 'error', moderate: 'warning', minor: 'notice'};

  // The alpha of a CSS colour as getComputedStyle writes it: "transparent",
  // rgb()/rgba() with commas, or any function with a "/ alpha" part
  // (rgb(0 0 0 / 0.5), color(srgb 1 0 0 / 0), oklch(0.5 0.1 20 / 50%)).
  // A colour with no alpha part is opaque. An alpha that does not parse
  // (CSS Color 4 "none", say) is NaN, which the walk treats as unreadable.
  const alpha = c => {
    if (!c || c === 'transparent') return 0;
    const inner = /^[a-z-]+\((.*)\)$/i.exec(c.trim());
    if (!inner) return 1;
    const slash = inner[1].split('/');
    const commas = inner[1].split(',');
    let a;
    if (slash.length > 1) a = slash.at(-1);
    else if (commas.length === 4) a = commas[3];
    else return 1;
    a = a.trim();
    return a.endsWith('%') ? Number.parseFloat(a) / 100 : Number.parseFloat(a);
  };

  // The colour pair of an element, read off the computed styles.
  const computedPair = element => {
    if (!element) return {source: 'undetermined', by: 'no element'};
    const fg = window.getComputedStyle(element).color;
    for (let e = element; e?.nodeType === 1; e = e.parentElement) {
      const style = window.getComputedStyle(e);
      if (style.backgroundImage && style.backgroundImage !== 'none') return {source: 'undetermined', by: 'background image'};
      if (Number.parseFloat(style.opacity) < 1) return {source: 'undetermined', by: 'opacity'};
      const a = alpha(style.backgroundColor);
      if (Number.isNaN(a)) return {source: 'undetermined', by: 'unreadable alpha'};
      if (a > 0 && a < 1) return {source: 'undetermined', by: 'translucent background'};
      if (a >= 1) return {source: 'computed', fg, bg: style.backgroundColor};
    }
    return {source: 'computed', fg, bg: 'canvas'};
  };

  const issues = [];
  const add = (result, needsFurtherReview) => {
    for (const node of result.nodes) {
      const target = node.target.map(t => (Array.isArray(t) ? t.join(' ') : t)).join(' ');
      const checks = [...(node.any || []), ...(node.all || []), ...(node.none || [])];
      const datas = checks.map(c => c.data).filter(d => d && typeof d === 'object');
      const axeColours = datas.find(d => d.fgColor && d.bgColor);
      const why = datas.find(d => d.messageKey);
      // Only a plain selector in the page's own document is resolved. A frame
      // or shadow-root target is a list of selectors, one per document; its
      // joined string is a descendant selector of the light DOM that may
      // match another element, so such a node has no element here.
      let element = null;
      if (node.target.length === 1 && typeof node.target[0] === 'string') {
        try {
          element = window.document.querySelector(node.target[0]);
        } catch {
          element = null;
        }
      }
      const extras = {impact: result.impact, needsFurtherReview, target, messageKey: why ? why.messageKey : null};
      if (result.id === 'color-contrast') {
        const pair = axeColours ? {source: 'axe', fg: axeColours.fgColor, bg: axeColours.bgColor} : computedPair(element);
        Object.assign(extras, {
          colourSource: pair.source,
          fgColor: pair.fg || null,
          bgColor: pair.bg || null,
          undeterminedBy: pair.by || null
        });
      }
      issues.push({
        element,
        code: result.id,
        type: level[result.impact] || 'error',
        message: `${result.help} (${result.helpUrl})`,
        runnerExtras: extras
      });
    }
  };
  violations.forEach(r => add(r, false));
  incomplete.forEach(r => add(r, true));
  return issues;
};

module.exports = {
  run,
  scripts: [`${axePath}/axe.min.js`],
  supports: '^10.0.0'
};
