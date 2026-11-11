'use strict';
// The unit test of axe-runner.js, run by the docs-portal CI job with
// node:test and held to 100 % line, branch and function coverage:
//
//   node --test --experimental-test-coverage \
//     --test-coverage-include=scripts/a11y-tools/axe-runner.js \
//     --test-coverage-lines=100 --test-coverage-branches=100 \
//     --test-coverage-functions=100 scripts/a11y-tools/axe-runner.test.js
//
// The runner's run function is evaluated in the page by pa11y; here it runs
// against a fake window: a stubbed axe.run, document.querySelector and
// getComputedStyle over plain objects linked by parentElement.
const test = require('node:test');
const assert = require('node:assert/strict');
const runner = require('./axe-runner.js');

// el(style, parent): a fake element whose computed style is style.
const el = (style, parent = null) => ({nodeType: 1, style, parentElement: parent});
const opaque = {backgroundColor: 'rgb(255, 255, 255)', backgroundImage: 'none', opacity: '1'};
const clear = {backgroundColor: 'rgba(0, 0, 0, 0)', backgroundImage: 'none', opacity: '1'};

// page(results, elements): install a window whose axe returns results and
// whose document resolves a selector through elements; returns what axe.run
// was called with.
const page = (results, elements = {}) => {
  const calls = [];
  globalThis.window = {
    document: {
      querySelector(sel) {
        if (sel === 'bad[') throw new Error('not a selector');
        return elements[sel] || null;
      }
    },
    getComputedStyle: e => e.style,
    axe: {
      run: async (context, options) => {
        calls.push({context, options});
        return results;
      }
    }
  };
  return calls;
};

const node = (target, data) => ({target, any: data === undefined ? undefined : [{data}]});
const contrast = (...nodes) => ({id: 'color-contrast', impact: 'serious', help: 'h', helpUrl: 'u', nodes});
const opts = {standard: 'WCAG2AA'};

test('a standard other than WCAG2AA is refused', async () => {
  page({});
  await assert.rejects(runner.run({standard: 'WCAG2A'}), /standard WCAG2A is not WCAG2AA/);
});

test('axe runs on the document with the WCAG 2.0/2.1 A/AA tags, best practices and the named rules', async () => {
  const calls = page({});
  assert.deepEqual(await runner.run({standard: 'WCAG2AA', rules: ['target-size'], ignore: ['region']}), []);
  assert.equal(calls[0].context, globalThis.window.document);
  assert.deepEqual(calls[0].options, {
    runOnly: {type: 'tags', values: ['wcag2a', 'wcag21a', 'wcag2aa', 'wcag21aa', 'best-practice']},
    rules: {'target-size': {enabled: true}, region: {enabled: false}}
  });
  page({violations: [], incomplete: []});
  await runner.run(opts);
});

test('the impact maps to the pa11y type, an unknown impact is an error, and needs-review is kept', async () => {
  const r = (id, impact) => ({id, impact, help: 'Help', helpUrl: 'https://x', nodes: [{target: ['p']}]});
  page({violations: [r('a', 'critical'), r('b', 'serious'), r('c', 'moderate'), r('d', 'minor'), r('e', 'odd')],
    incomplete: [r('f', 'minor')]});
  const issues = await runner.run(opts);
  assert.deepEqual(issues.map(i => [i.code, i.type, i.runnerExtras.needsFurtherReview]),
    [['a', 'error', false], ['b', 'error', false], ['c', 'warning', false], ['d', 'notice', false],
      ['e', 'error', false], ['f', 'notice', true]]);
  assert.equal(issues[0].message, 'Help (https://x)');
  assert.equal(issues[0].runnerExtras.colourSource, undefined, 'only color-contrast carries colours');
});

test('a frame or shadow target is joined into one string, and an unresolvable one has no element', async () => {
  page({violations: [{id: 'region', impact: 'moderate', nodes: [{target: [['#frame', 'p'], 'span']}, {target: ['bad[']}]}]});
  const [joined, bad] = await runner.run(opts);
  assert.equal(joined.runnerExtras.target, '#frame p span');
  assert.equal(joined.element, null);
  assert.equal(bad.element, null);
});

test('axe colours win over the computed ones, and messageKey is kept', async () => {
  const p = el({...opaque, color: 'rgb(1, 1, 1)'});
  page({violations: [contrast({target: ['p'], any: [{data: null}, {data: 'x'}, {data: {messageKey: 'k'}}],
    all: [{data: {fgColor: '#111', bgColor: '#fff'}}], none: []})]}, {p});
  const [i] = await runner.run(opts);
  assert.deepEqual(i.runnerExtras, {impact: 'serious', needsFurtherReview: false, target: 'p', messageKey: 'k',
    colourSource: 'axe', fgColor: '#111', bgColor: '#fff', undeterminedBy: null});
  assert.equal(i.element, p);
});

test('without axe colours the pair is the computed colour and the nearest painted background', async () => {
  const body = el(opaque);
  const div = el(clear, body);
  const span = el({...clear, color: 'rgb(9, 9, 9)'}, div);
  page({incomplete: [contrast(node(['span'], {messageKey: 'elmPartiallyObscured'}))]}, {span});
  const [i] = await runner.run(opts);
  assert.equal(i.runnerExtras.colourSource, 'computed');
  assert.equal(i.runnerExtras.fgColor, 'rgb(9, 9, 9)');
  assert.equal(i.runnerExtras.bgColor, 'rgb(255, 255, 255)');
  assert.equal(i.runnerExtras.undeterminedBy, null);
  assert.equal(i.runnerExtras.messageKey, 'elmPartiallyObscured');
});

test('a walk that meets no painted background ends at the canvas, also at a non-element parent', async () => {
  const doc = {nodeType: 9, style: opaque, parentElement: null};
  const top = el({color: 'red'}, doc);
  page({incomplete: [contrast(node(['top']))]}, {top});
  const [i] = await runner.run(opts);
  assert.deepEqual([i.runnerExtras.colourSource, i.runnerExtras.fgColor, i.runnerExtras.bgColor], ['computed', 'red', 'canvas']);
});

test('a background image, an opacity below 1 or a translucent background make the pair undetermined', async () => {
  const cases = [
    [{...clear, backgroundImage: 'linear-gradient(red, blue)'}, 'background image'],
    [{...clear, opacity: '0.5'}, 'opacity'],
    [{...clear, backgroundColor: 'rgba(212, 31, 28, 0.07)'}, 'translucent background']
  ];
  for (const [style, by] of cases) {
    const span = el({...clear, color: 'red'}, el(style, el(opaque)));
    page({incomplete: [contrast(node(['span']))]}, {span});
    const [i] = await runner.run(opts);
    assert.deepEqual([i.runnerExtras.colourSource, i.runnerExtras.fgColor, i.runnerExtras.bgColor, i.runnerExtras.undeterminedBy],
      ['undetermined', null, null, by], by);
  }
});

test('a node with no element is undetermined', async () => {
  page({incomplete: [contrast(node(['#gone']))]});
  const [i] = await runner.run(opts);
  assert.deepEqual([i.runnerExtras.colourSource, i.runnerExtras.fgColor, i.runnerExtras.bgColor, i.runnerExtras.undeterminedBy],
    ['undetermined', null, null, 'no element']);
});

test('the alpha of each colour syntax: zero is transparent, one is painted, between is translucent', async () => {
  const cases = [
    ['', 'canvas'], // no background colour at all
    ['transparent', 'canvas'],
    ['rgba(0, 0, 0, 0.0)', 'canvas'],
    ['rgb(0 0 0 / 0)', 'canvas'],
    ['color(srgb 1 0 0 / 0)', 'canvas'],
    ['oklch(0.5 0.1 20 / 0%)', 'canvas'],
    ['rgb(1, 2, 3)', 'rgb(1, 2, 3)'],
    ['rgba(1, 2, 3, 1)', 'rgba(1, 2, 3, 1)'],
    ['oklch(0.5 0.1 20 / 100%)', 'oklch(0.5 0.1 20 / 100%)'],
    ['color(srgb 1 0 0)', 'color(srgb 1 0 0)'],
    ['white', 'white'],
    ['oklch(0.5 0.1 20 / 40%)', null],
    ['rgb(0 0 0 / 0.5)', null]
  ];
  for (const [bg, want] of cases) {
    const span = el({color: 'red', backgroundColor: bg, opacity: '1'});
    page({incomplete: [contrast(node(['span']))]}, {span});
    const [i] = await runner.run(opts);
    assert.equal(i.runnerExtras.bgColor, want, `background ${JSON.stringify(bg)}`);
  }
});

test('the module names the axe script pa11y injects and the pa11y versions it supports', () => {
  assert.match(runner.scripts[0], /axe-core[\\/]axe\.min\.js$/);
  assert.equal(runner.supports, '^10.0.0');
});
