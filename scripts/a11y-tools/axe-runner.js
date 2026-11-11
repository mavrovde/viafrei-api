'use strict';
// A pa11y runner for scripts/docs-a11y.sh: axe-core, run as pa11y's own axe
// runner runs it (WCAG 2.0 and 2.1 A/AA tags and best practices, rules
// enabled or disabled by name), with additions in runnerExtras that the
// built-in runner drops:
//
// - target: axe's selector for the node, the key a non-colour result is
//   counted by (docs-a11y.sh strips its nth-child parts and indices);
// - fgColor and bgColor: the colour pair a color-contrast result is counted
//   by. When axe computed them they are axe's (colourSource "axe"). When axe
//   could not (a "needs review" result: a background gradient, an element
//   partly obscured), they are the element's computed text colour and the
//   background colour of the nearest element at or above it that paints one
//   (colourSource "computed"); null when the node has no element;
// - messageKey: why axe could not decide, when it says.
//
// The run function is stringified by pa11y and evaluated in the page, so it
// may use nothing from this module's scope.
const path = require('path');

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
  const transparent = c => !c || c === 'transparent' || /^rgba\(.*,\s*0\)$/.test(c);
  const computedPair = element => {
    if (!element) return [null, null];
    const fg = window.getComputedStyle(element).color;
    let bg = null;
    for (let e = element; e && e.nodeType === 1 && bg === null; e = e.parentElement) {
      const c = window.getComputedStyle(e).backgroundColor;
      if (!transparent(c)) bg = c;
    }
    return [fg, bg || 'canvas'];
  };
  const issues = [];
  const add = (result, needsFurtherReview) => {
    for (const node of result.nodes) {
      const target = node.target.map(t => (Array.isArray(t) ? t.join(' ') : t)).join(' ');
      const checks = [...(node.any || []), ...(node.all || []), ...(node.none || [])];
      const datas = checks.map(c => c.data).filter(d => d && typeof d === 'object');
      const axeColours = datas.find(d => d.fgColor && d.bgColor);
      const why = datas.find(d => d.messageKey);
      let element = null;
      try {
        element = window.document.querySelector(target);
      } catch (e) {
        element = null;
      }
      const extras = {impact: result.impact, needsFurtherReview, target, messageKey: why ? why.messageKey : null};
      if (result.id === 'color-contrast') {
        const [fg, bg] = axeColours ? [axeColours.fgColor, axeColours.bgColor] : computedPair(element);
        Object.assign(extras, {fgColor: fg, bgColor: bg, colourSource: axeColours ? 'axe' : 'computed'});
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
