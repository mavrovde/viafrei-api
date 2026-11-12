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
//     A partly transparent background is composited onto the opaque one
//     below it. contrastRatio is the pair's WCAG contrast and requiredRatio
//     what WCAG 1.4.3 asks of the element (3 for large text, else 4.5);
//     docs-a11y.sh counts a needs-review result whose computed pair meets
//     its ratio as decided, not as a failure;
//   - "undetermined": no pair can be read off the styles: the node has no
//     element, or the walk met a background image, an opacity below 1 or a
//     background colour that is not readable as sRGB. fgColor and bgColor
//     are null, and undeterminedBy says which.
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

  // The red, green, blue and alpha of an rgb()/rgba() colour with commas,
  // the form getComputedStyle writes for sRGB colours; null for any other.
  const rgba = c => {
    const m = /^rgba?\(\s*([\d.]+),\s*([\d.]+),\s*([\d.]+)(?:,\s*([\d.]+))?\s*\)$/.exec(c);
    return m ? [Number(m[1]), Number(m[2]), Number(m[3]), m[4] === undefined ? 1 : Number(m[4])] : null;
  };
  // over(top, bottom): the opaque colour of a translucent top layer painted
  // on an opaque bottom one (source-over compositing).
  const over = (top, bottom) => [0, 1, 2].map(i => top[i] * top[3] + bottom[i] * (1 - top[3]));
  const css = c => `rgb(${c.map(v => Math.round(v)).join(', ')})`;
  const white = [255, 255, 255, 1];

  // The colour pair of an element, read off the computed styles. A partly
  // transparent background is composited onto the first opaque background
  // below it (or the white canvas), when every layer is an sRGB colour.
  const computedPair = element => {
    if (!element) return {source: 'undetermined', by: 'no element'};
    const fg = window.getComputedStyle(element).color;
    const layers = [];
    let bg = null;
    for (let e = element; !bg && e?.nodeType === 1; e = e.parentElement) {
      const style = window.getComputedStyle(e);
      if (style.backgroundImage && style.backgroundImage !== 'none') return {source: 'undetermined', by: 'background image'};
      if (Number.parseFloat(style.opacity) < 1) return {source: 'undetermined', by: 'opacity'};
      const a = alpha(style.backgroundColor);
      if (Number.isNaN(a)) return {source: 'undetermined', by: 'unreadable alpha'};
      if (a >= 1) bg = style.backgroundColor;
      else if (a > 0) layers.push(rgba(style.backgroundColor));
    }
    if (!layers.length) return {source: 'computed', fg, bg: bg || 'canvas'};
    const base = bg ? rgba(bg) : white;
    if (!base || layers.includes(null)) return {source: 'undetermined', by: 'translucent background'};
    return {source: 'computed', fg, bg: css(layers.reduceRight((b, top) => over(top, b), base))};
  };

  // The contrast of a computed pair (WCAG 2 relative luminance, the text
  // composited onto the background when it is partly transparent) and the
  // ratio WCAG 1.4.3 requires of the element: 3 for large text (24 px, or
  // 18.66 px at weight 700 or more), else 4.5. Both are null when a colour
  // is not an sRGB rgb()/rgba() value.
  const lum = c =>
    [0, 1, 2]
      .map(i => c[i] / 255)
      .map(v => (v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4))
      .reduce((s, v, i) => s + v * [0.2126, 0.7152, 0.0722][i], 0);
  const contrast = (element, pair) => {
    const fg = rgba(pair.fg);
    const bg = pair.bg === 'canvas' ? white : rgba(pair.bg);
    if (!fg || !bg) return {ratio: null, required: null};
    const l1 = lum(over(fg, bg));
    const l2 = lum(bg);
    const style = window.getComputedStyle(element);
    const size = Number.parseFloat(style.fontSize);
    const large = size >= 24 || (Number.parseInt(style.fontWeight, 10) >= 700 && size >= 18.66);
    return {ratio: Math.round(((Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05)) * 100) / 100, required: large ? 3 : 4.5};
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
        const measured = pair.source === 'computed' ? contrast(element, pair) : {ratio: null, required: null};
        Object.assign(extras, {
          colourSource: pair.source,
          fgColor: pair.fg || null,
          bgColor: pair.bg || null,
          undeterminedBy: pair.by || null,
          contrastRatio: measured.ratio,
          requiredRatio: measured.required
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
