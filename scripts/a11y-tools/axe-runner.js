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
//     as text partly obscured), so the element's computed text colour and
//     the colour the page paints under it, read off the page's paint stack
//     at the text (see computedPair), or "canvas" when nothing below it
//     paints. A partly transparent background is composited onto the opaque
//     one below it. contrastRatio is the pair's WCAG contrast and requiredRatio
//     what WCAG 1.4.3 asks of the element (3 for large text, else 4.5);
//     docs-a11y.sh counts a needs-review result whose computed pair meets
//     its ratio as decided, not as a failure;
//   - "undetermined": no pair can be read: the node has no element, or is
//     not rendered or not where its box is, something is painted over its
//     text, or a layer under it has a background image, a pseudo-element
//     background, an opacity below 1, a filter or a blend mode, its border
//     at the text, or a background colour that is not readable as sRGB. fgColor and bgColor
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
  // (CSS Color 4 "none", say) is NaN, which the reader treats as unreadable.
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

  // A ::before or ::after that paints a background. elementsFromPoint
  // reports a pseudo-element as the element it belongs to, so its colour
  // cannot be told from the element's own.
  const paintsBackground = e =>
    ['::before', '::after'].some(pseudo => {
      const st = window.getComputedStyle(e, pseudo);
      return !['none', 'normal'].includes(st.content) &&
        (alpha(st.backgroundColor) !== 0 || st.backgroundImage !== 'none');
    });
  // Whether (x, y) falls on a border of e that is drawn: a side with a
  // width, a style other than none or hidden, and a colour that is not
  // fully transparent.
  const onBorder = (e, st, x, y) => {
    const r = e.getBoundingClientRect();
    const drawn = side => {
      const width = Number.parseFloat(st[`border${side}Width`]) || 0;
      const visible = !['none', 'hidden', undefined].includes(st[`border${side}Style`]) && alpha(st[`border${side}Color`]) !== 0;
      return visible ? width : 0;
    };
    const [left, right, top, bottom] = ['Left', 'Right', 'Top', 'Bottom'].map(drawn);
    return (left > 0 && x < r.left + left) || (right > 0 && x > r.right - right) ||
      (top > 0 && y < r.top + top) || (bottom > 0 && y > r.bottom - bottom);
  };
  const undetermined = by => ({source: 'undetermined', by});
  // An effect that changes the colour painted, which the colours read off
  // the styles do not show: an opacity below 1, a filter or backdrop filter,
  // or a blend mode other than normal.
  const effect = st => {
    if (Number.parseFloat(st.opacity) < 1) return 'opacity';
    if (![undefined, 'none'].includes(st.filter) || ![undefined, 'none'].includes(st.backdropFilter)) return 'filter';
    if (![undefined, 'normal'].includes(st.mixBlendMode)) return 'blend mode';
    return null;
  };

  // The colour painted under element's text at (x, y), read off the page's
  // own paint stack there (document.elementsFromPoint): {bg} or, when it
  // cannot be read, {by}.
  // - the element must be in the stack, with nothing above it but its own
  //   descendants painting no background (nothing painted over the text);
  // - from the element down, each layer's background colour is read until
  //   the first opaque one (else the white canvas), and the translucent
  //   ones are composited onto it, when every one is an sRGB colour. Any
  //   other element under the text, an ancestor or not, is a layer.
  // A layer with a background image, a ::before or ::after background, an
  // effect (below), a drawn border at the point, a background colour whose
  // alpha cannot be read, or a translucent colour that is not sRGB makes it
  // unreadable.
  const background = (element, x, y) => {
    const stack = window.document.elementsFromPoint(x, y);
    const at = stack.indexOf(element);
    if (at < 0) return {by: 'not at its own position'};
    const paints = e => {
      const st = window.getComputedStyle(e);
      return alpha(st.backgroundColor) !== 0 || st.backgroundImage !== 'none' || paintsBackground(e);
    };
    if (stack.slice(0, at).some(e => !element.contains(e) || paints(e))) return {by: 'painted over'};
    const layers = [];
    let bg = null;
    for (const e of stack.slice(at)) {
      const st = window.getComputedStyle(e);
      if (st.backgroundImage && st.backgroundImage !== 'none') return {by: 'background image'};
      if (paintsBackground(e)) return {by: 'pseudo-element background'};
      const fx = effect(st);
      if (fx) return {by: fx};
      if (onBorder(e, st, x, y)) return {by: 'border'};
      const a = alpha(st.backgroundColor);
      if (Number.isNaN(a)) return {by: 'unreadable alpha'};
      if (a >= 1) {
        bg = st.backgroundColor;
        break;
      }
      if (a > 0) layers.push(rgba(st.backgroundColor));
    }
    if (!layers.length) return {bg: bg || 'canvas'};
    const base = bg ? rgba(bg) : white;
    if (!base || layers.includes(null)) return {by: 'translucent background'};
    return {bg: css(layers.reduceRight((b, top) => over(top, b), base))};
  };

  // The colour pair of an element: its computed text colour, and of the
  // colours painted under its text the one it contrasts least with. The
  // element is scrolled into view and the part of its first box that is
  // shown (inside the viewport and every ancestor that clips its overflow,
  // such as a scrolling code block) is sampled at five points, its centre
  // and the centres of its four quarters, so text that straddles two
  // backgrounds is held to the worse one (points nearer the edge would land
  // on the neighbouring glyphs of inline text); every element is
  // made hit-testable while the stacks are read, so a layer with
  // pointer-events: none is not skipped. Undetermined: no element, an
  // effect on it or an ancestor, an empty box, a box clipped
  // entirely, or a point whose
  // colour cannot be read (background, above).
  const computedPair = element => {
    if (!element) return undetermined('no element');
    const fg = window.getComputedStyle(element).color;
    for (let e = element; e?.nodeType === 1; e = e.parentElement) {
      const fx = effect(window.getComputedStyle(e));
      if (fx) return undetermined(fx);
    }
    element.scrollIntoView({block: 'center', inline: 'center'});
    const box = element.getClientRects()[0];
    if (!box || !box.width || !box.height) return undetermined('not rendered');
    const shown = {left: box.left, top: box.top, right: box.left + box.width, bottom: box.top + box.height};
    const cut = r => {
      shown.left = Math.max(shown.left, r.left);
      shown.top = Math.max(shown.top, r.top);
      shown.right = Math.min(shown.right, r.right);
      shown.bottom = Math.min(shown.bottom, r.bottom);
    };
    cut({left: 0, top: 0, right: window.innerWidth, bottom: window.innerHeight});
    for (let e = element.parentElement; e?.nodeType === 1; e = e.parentElement) {
      const st = window.getComputedStyle(e);
      if ([st.overflowX, st.overflowY].some(v => ['hidden', 'auto', 'scroll', 'clip'].includes(v))) cut(e.getBoundingClientRect());
    }
    if (shown.right <= shown.left || shown.bottom <= shown.top) return undetermined('clipped');
    const along = (start, end) => [(start + end) / 2, start + (end - start) / 4, start + (3 * (end - start)) / 4];
    const [xc, x0, x1] = along(shown.left, shown.right);
    const [yc, y0, y1] = along(shown.top, shown.bottom);
    const lift = window.document.createElement('style');
    lift.textContent = '*{pointer-events:auto!important}';
    window.document.head.appendChild(lift);
    const found = [[xc, yc], [x0, y0], [x1, y0], [x0, y1], [x1, y1]].map(([x, y]) => background(element, x, y));
    lift.remove();
    const unreadable = found.find(f => f.by);
    if (unreadable) return undetermined(unreadable.by);
    const ratio = bg => contrast(element, {fg, bg}).ratio ?? -1;
    const worst = found.map(f => f.bg).reduce((w, bg) => (ratio(bg) < ratio(w) ? bg : w));
    return {source: 'computed', fg, bg: worst};
  };

  // The contrast of a computed pair (WCAG 2 relative luminance, the text
  // composited onto the background when it is partly transparent) and the
  // ratio WCAG 1.4.3 requires of the element: 3 for large text (24 px, or
  // 18.66 px at weight 700 or more), else 4.5. Both are null when a colour
  // is not an sRGB rgb()/rgba() value. The ratio is rounded DOWN to two
  // places, so a pair that misses its requirement by any amount can never
  // read as meeting it (4.4999 is 4.49, not 4.50).
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
    return {ratio: Math.floor(((Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05)) * 100) / 100, required: large ? 3 : 4.5};
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
