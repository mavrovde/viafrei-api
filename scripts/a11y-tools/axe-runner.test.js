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
// against a fake window: a stubbed axe.run, document.querySelector,
// document.elementsFromPoint and getComputedStyle over plain objects linked
// by parentElement. By default an element has one 10 px box at the origin,
// and the stack at a point is the element last scrolled into view and its
// ancestors; window.stack replaces that stack, and window.stackAt(x, y)
// gives one per point. window.points lists the points asked, and
// window.lifted records
// whether the pointer-events override was in the page when the stack was
// read.
const test = require('node:test');
const assert = require('node:assert/strict');
const runner = require('./axe-runner.js');

// el(style, parent, pseudo): a fake element whose computed style is style
// and whose ::before/::after styles are pseudo's (none by default).
const el = (style, parent = null, pseudo = {}) => ({
  nodeType: 1,
  style,
  parentElement: parent,
  pseudo,
  rects: [{left: 0, top: 0, width: 10, height: 10}],
  bounds: {left: 0, top: 0, right: 10, bottom: 10},
  getBoundingClientRect() {
    return this.bounds;
  },
  contains(other) {
    for (let e = other; e; e = e.parentElement) if (e === this) return true;
    return false;
  },
  getClientRects() {
    return this.rects;
  },
  scrollIntoView() {
    globalThis.window.scrolled = this;
  }
});
const chain = e => (e?.nodeType === 1 ? [e, ...chain(e.parentElement)] : []);
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
      },
      elementsFromPoint: (x, y) => {
        const w = globalThis.window;
        w.points.push([x, y]);
        w.readLifted = w.lifted;
        return w.stackAt ? w.stackAt(x, y) : w.stack || chain(w.scrolled);
      },
      createElement: tag => ({
        tag,
        remove() {
          globalThis.window.lifted = null;
        }
      }),
      head: {
        appendChild(e) {
          globalThis.window.lifted = `${e.tag} ${e.textContent}`;
        }
      }
    },
    points: [],
    innerWidth: 1000,
    innerHeight: 800,
    getComputedStyle: (e, pseudo) => (pseudo ? e.pseudo[pseudo] || {content: 'none'} : e.style),
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
  assert.deepEqual(await runner.run(opts), []);
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

test('a shadow target is never resolved in the light DOM, even when its joined string matches there', async () => {
  const light = el({...opaque, color: 'rgb(1, 1, 1)'});
  page({incomplete: [contrast({target: [['host', 'inner']]}), contrast({target: ['host', 'inner']})]},
    {'host inner': light, host: light, inner: light});
  const [shadow, frame] = await runner.run(opts);
  assert.equal(shadow.runnerExtras.target, 'host inner');
  assert.equal(shadow.element, null);
  assert.equal(shadow.runnerExtras.undeterminedBy, 'no element');
  assert.equal(frame.element, null);
  assert.equal(frame.runnerExtras.undeterminedBy, 'no element');
});

test('axe colours win over the computed ones, and messageKey is kept', async () => {
  const p = el({...opaque, color: 'rgb(1, 1, 1)'});
  page({violations: [contrast({target: ['p'], any: [{data: null}, {data: 'x'}, {data: {messageKey: 'k'}}],
    all: [{data: {fgColor: '#111', bgColor: '#fff'}}], none: []})]}, {p});
  const [i] = await runner.run(opts);
  assert.deepEqual(i.runnerExtras, {impact: 'serious', needsFurtherReview: false, target: 'p', messageKey: 'k',
    colourSource: 'axe', fgColor: '#111', bgColor: '#fff', undeterminedBy: null, contrastRatio: null, requiredRatio: null});
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

test('a background image, an opacity below 1 or a translucent background that is not sRGB make the pair undetermined', async () => {
  const cases = [
    [{...clear, backgroundImage: 'linear-gradient(red, blue)'}, 'background image'],
    [{...clear, opacity: '0.5'}, 'opacity'],
    [{...clear, backgroundColor: 'oklch(0.5 0.1 20 / 7%)'}, 'translucent background']
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
    ['oklch(0.5 0.1 20 / 40%)', null, 'translucent background'],
    ['rgb(0 0 0 / 0.5)', null, 'translucent background'],
    ['oklch(0.5 0.1 20 / none)', null, 'unreadable alpha'],
    ['rgba(0, 0, 0, none)', null, 'unreadable alpha']
  ];
  for (const [bg, want, by = null] of cases) {
    const span = el({color: 'red', backgroundColor: bg, opacity: '1'}, el(opaque));
    page({incomplete: [contrast(node(['span']))]}, {span});
    const [i] = await runner.run(opts);
    assert.equal(i.runnerExtras.bgColor, want === 'canvas' ? 'rgb(255, 255, 255)' : want, `background ${JSON.stringify(bg)}`);
    assert.equal(i.runnerExtras.undeterminedBy, by, `background ${JSON.stringify(bg)}`);
  }
});

test('the module names the axe script pa11y injects and the pa11y versions it supports', () => {
  assert.match(runner.scripts[0], /axe-core[\\/]axe\.min\.js$/);
  assert.equal(runner.supports, '^10.0.0');
});

// one(span): run the runner over one color-contrast node resolving to span.
const one = async span => {
  page({incomplete: [contrast(node(['span']))]}, {span});
  const [i] = await runner.run(opts);
  return i.runnerExtras;
};

test('a translucent sRGB background is composited onto the opaque one below it, or onto the white canvas', async () => {
  const green = await one(el({...clear, color: 'rgb(29, 129, 39)', backgroundColor: 'rgba(29, 129, 39, 0.07)'}, el(opaque)));
  assert.deepEqual([green.colourSource, green.bgColor, green.undeterminedBy], ['computed', 'rgb(239, 246, 240)', null]);
  // Two half-black layers and no opaque one: the lower is painted first, on white.
  const grey = await one(el({...clear, color: 'rgb(0, 0, 0)', backgroundColor: 'rgba(0, 0, 0, 0.5)'},
    el({...clear, backgroundColor: 'rgba(0, 0, 0, 0.5)'}, {nodeType: 9, style: opaque, parentElement: null})));
  assert.equal(grey.bgColor, 'rgb(64, 64, 64)');
  // The opaque colour below is not sRGB: nothing to composite onto.
  const named = await one(el({...clear, color: 'red', backgroundColor: 'rgba(0, 0, 0, 0.5)'}, el({...opaque, backgroundColor: 'white'})));
  assert.deepEqual([named.colourSource, named.bgColor, named.undeterminedBy], ['undetermined', null, 'translucent background']);
});

test('a computed pair carries its WCAG contrast; a colour that is not sRGB has none', async () => {
  const black = await one(el({...clear, color: 'rgb(0, 0, 0)'}, el(opaque)));
  assert.deepEqual([black.contrastRatio, black.requiredRatio], [21, 4.5]);
  const same = await one(el({...clear, color: 'rgb(255, 255, 255)'}, el(opaque)));
  assert.equal(same.contrastRatio, 1);
  const canvas = await one(el({color: 'rgb(0, 0, 0)'}, {nodeType: 9, style: opaque, parentElement: null}));
  assert.deepEqual([canvas.bgColor, canvas.contrastRatio], ['canvas', 21]);
  // Half-transparent black text is grey on white: 3.979:1, read as 3.97 (rounded down), under 4.5.
  const half = await one(el({...clear, color: 'rgba(0, 0, 0, 0.5)'}, el(opaque)));
  assert.equal(half.contrastRatio, 3.97);
  const named = await one(el({...clear, color: 'red'}, el(opaque)));
  assert.deepEqual([named.contrastRatio, named.requiredRatio], [null, null]);
  const namedBg = await one(el({...clear, color: 'rgb(0, 0, 0)'}, el({...opaque, backgroundColor: 'white'})));
  assert.deepEqual([namedBg.contrastRatio, namedBg.requiredRatio], [null, null]);
  // An axe pair or an undetermined one is not measured here.
  page({incomplete: [contrast(node(['#gone']), node(['x'], {fgColor: '#111', bgColor: '#fff'}))]});
  for (const i of await runner.run(opts)) assert.deepEqual([i.runnerExtras.contrastRatio, i.runnerExtras.requiredRatio], [null, null]);
});

test('large text (24 px, or 18.66 px at weight 700) needs 3:1, other text 4.5:1', async () => {
  const cases = [
    [{fontSize: '24px', fontWeight: '400'}, 3],
    [{fontSize: '18.66px', fontWeight: '700'}, 3],
    [{fontSize: '18.66px', fontWeight: '400'}, 4.5],
    [{fontSize: '18px', fontWeight: '700'}, 4.5],
    [{fontSize: '16px', fontWeight: 'bold'}, 4.5]
  ];
  for (const [font, want] of cases) {
    const x = await one(el({...clear, color: 'rgb(0, 0, 0)', ...font}, el(opaque)));
    assert.equal(x.requiredRatio, want, JSON.stringify(font));
  }
});

test('the ratio is rounded down: a pair just under 4.5 never reads as 4.5', async () => {
  // Translucent black text on white composites to a grey of ratio 4.4954.
  const i = await one(el({...opaque, color: 'rgba(0, 0, 0, 0.5344)'}));
  assert.deepEqual([i.contrastRatio, i.requiredRatio], [4.49, 4.5]);
});

test('a background painted by a ::before or ::after on the walk makes the pair undetermined', async () => {
  const painting = [
    {content: '""', backgroundColor: 'rgb(124, 124, 187)', backgroundImage: 'none'},
    {content: '""', backgroundColor: 'rgba(0, 0, 0, 0)', backgroundImage: 'url(x.svg)'},
    {content: '"x"', backgroundColor: 'oklch(0.5 0.1 20 / none)', backgroundImage: 'none'}
  ];
  for (const style of painting) {
    for (const where of ['::before', '::after']) {
      const td = el(opaque, null, {[where]: style});
      const i = await one(el({...clear, color: 'rgb(0, 0, 0)'}, td));
      assert.deepEqual([i.colourSource, i.undeterminedBy], ['undetermined', 'pseudo-element background'], `${where} ${JSON.stringify(style)}`);
    }
  }
  // A pseudo-element that paints nothing, or is not generated, is no layer:
  // a tree line drawn as a border, or a background with no content.
  const drawn = {content: '""', backgroundColor: 'rgba(0, 0, 0, 0)', backgroundImage: 'none', borderLeft: '1px solid'};
  const unrendered = {content: 'none', backgroundColor: 'rgb(0, 0, 0)', backgroundImage: 'none'};
  const normal = {content: 'normal', backgroundColor: 'rgb(0, 0, 0)', backgroundImage: 'none'};
  const td = el(opaque, null, {'::before': drawn, '::after': unrendered});
  const ok = await one(el({...clear, color: 'rgb(0, 0, 0)'}, td, {'::before': normal}));
  assert.deepEqual([ok.colourSource, ok.contrastRatio], ['computed', 21]);
});

test('the background is read off the page\'s paint stack at the text, ancestors or not', async () => {
  const body = el(opaque);
  const div = el(clear, body);
  const span = el({...clear, color: 'rgb(0, 0, 0)'}, div);
  const child = el(clear, span);
  const paintedChild = el({...clear, backgroundColor: 'rgb(9, 9, 9)'}, span);
  const other = el({...opaque, backgroundColor: 'rgb(17, 23, 26)'}, body);
  const veil = el({...clear, backgroundColor: 'rgba(0, 0, 0, 0.5)'}, body);
  const framed = el({...clear, borderLeftWidth: '6px', borderLeftStyle: 'solid', borderLeftColor: 'rgb(0, 0, 0)'}, body);
  const cases = [
    [[child, span, div, body], null, 'rgb(255, 255, 255)'],
    [[span, div, body, other], null, 'rgb(255, 255, 255)'], // under the first opaque layer: hidden by it
    [[span, other, div, body], null, 'rgb(17, 23, 26)'], // a positioned sibling under the text is a layer
    [[span, veil, div, body], null, 'rgb(128, 128, 128)'], // a translucent one is composited
    [[span, div, framed, body], 'border', null],
    [[other, span, div, body], 'painted over', null],
    [[paintedChild, span, div, body], 'painted over', null],
    [[div, body], 'not at its own position', null],
    [[], 'not at its own position', null]
  ];
  for (const [stack, by, bg] of cases) {
    page({incomplete: [contrast(node(['span']))]}, {span});
    globalThis.window.stack = stack;
    const [i] = await runner.run(opts);
    assert.deepEqual([i.runnerExtras.undeterminedBy, i.runnerExtras.bgColor], [by, bg], `stack ${by} ${bg}`);
    assert.equal(i.runnerExtras.colourSource, by ? 'undetermined' : 'computed');
    assert.equal(globalThis.window.scrolled, span, 'the element is scrolled into view first');
    assert.equal(globalThis.window.readLifted, 'style *{pointer-events:auto!important}', 'every element is hit-testable while the stack is read');
    assert.equal(globalThis.window.lifted, null, 'the override is removed after');
  }
  // The points asked are the centre of the element's first box and the
  // centres of its four quarters.
  span.rects = [{left: 10, top: 20, width: 30, height: 40}, {left: 0, top: 0, width: 1, height: 1}];
  page({incomplete: [contrast(node(['span']))]}, {span});
  globalThis.window.stack = [span];
  await runner.run(opts);
  assert.deepEqual(globalThis.window.points, [[25, 40], [17.5, 30], [32.5, 30], [17.5, 50], [32.5, 50]]);
  const small = el({...clear, color: 'rgb(0, 0, 0)'}, body);
  small.rects = [{left: 0, top: 0, width: 2, height: 2}];
  page({incomplete: [contrast(node(['span']))]}, {span: small});
  await runner.run(opts);
  assert.deepEqual(globalThis.window.points, [[1, 1], [0.5, 0.5], [1.5, 0.5], [0.5, 1.5], [1.5, 1.5]]);
  // A drawn border of a layer at a point, on each side; one that is not
  // drawn (no width, no style, hidden, transparent) is no border.
  const drawn = side => ({[`border${side}Width`]: '20px', [`border${side}Style`]: 'solid', [`border${side}Color`]: 'rgb(0, 0, 0)'});
  const borders = [
    ...['Left', 'Right', 'Top', 'Bottom'].map(side => [drawn(side), 'border']),
    [{...drawn('Left'), borderLeftWidth: '0px'}, null],
    [{...drawn('Left'), borderLeftStyle: 'none'}, null],
    [{...drawn('Left'), borderLeftStyle: 'hidden'}, null],
    [{...drawn('Left'), borderLeftStyle: undefined}, null],
    [{...drawn('Left'), borderLeftColor: 'rgba(0, 0, 0, 0)'}, null]
  ];
  for (const [style, by] of borders) {
    const edge = el({...clear, ...style}, body);
    edge.bounds = {left: 10, top: 20, right: 40, bottom: 60};
    page({incomplete: [contrast(node(['span']))]}, {span});
    globalThis.window.stack = [span, edge, body];
    const [i] = await runner.run(opts);
    assert.equal(i.runnerExtras.undeterminedBy, by, JSON.stringify(style));
  }
  for (const rects of [[], [{left: 0, top: 0, width: 0, height: 10}], [{left: 0, top: 0, width: 10, height: 0}]]) {
    span.rects = rects;
    page({incomplete: [contrast(node(['span']))]}, {span});
    const [i] = await runner.run(opts);
    assert.equal(i.runnerExtras.undeterminedBy, 'not rendered', JSON.stringify(rects));
  }
});

test('a layer\'s opacity, and an opacity on an ancestor that is no layer, make the pair undetermined', async () => {
  const body = el(opaque);
  const span = el({...clear, color: 'rgb(0, 0, 0)'}, body);
  const faded = el({...opaque, opacity: '0.9'});
  page({incomplete: [contrast(node(['span']))]}, {span});
  globalThis.window.stack = [span, faded, body];
  const [layer] = await runner.run(opts);
  assert.equal(layer.runnerExtras.undeterminedBy, 'opacity');
  const inner = el({...clear, color: 'rgb(0, 0, 0)'}, el({...clear, opacity: '0.5'}, body));
  page({incomplete: [contrast(node(['span']))]}, {span: inner});
  globalThis.window.stack = [inner, body];
  const [ancestor] = await runner.run(opts);
  assert.equal(ancestor.runnerExtras.undeterminedBy, 'opacity');
});

test('text over two backgrounds is held to the one it contrasts least with, and one unreadable point decides', async () => {
  const body = el(opaque);
  const dark = el({...opaque, backgroundColor: 'rgb(17, 23, 26)'}, body);
  const grey = el({...opaque, backgroundColor: 'rgb(118, 118, 118)'}, body);
  const pictured = el({...clear, backgroundImage: 'url(x.png)'}, body);
  const span = el({...clear, color: 'rgb(255, 255, 255)'}, body);
  span.rects = [{left: 0, top: 0, width: 40, height: 20}];
  const run = async stackAt => {
    page({incomplete: [contrast(node(['span']))]}, {span});
    globalThis.window.stackAt = stackAt;
    const [i] = await runner.run(opts);
    return i.runnerExtras;
  };
  // The top half over the dark layer, the bottom half over the grey one.
  const split = await run((x, y) => [span, y < 10 ? dark : grey, body]);
  assert.deepEqual([split.bgColor, split.contrastRatio], ['rgb(118, 118, 118)', 4.54]);
  const flipped = await run((x, y) => [span, y < 10 ? grey : dark, body]);
  assert.equal(flipped.bgColor, 'rgb(118, 118, 118)');
  // One corner over a background image: the pair cannot be read.
  const corner = await run((x, y) => [span, x >= 30 && y >= 15 ? pictured : dark, body]);
  assert.deepEqual([corner.colourSource, corner.undeterminedBy], ['undetermined', 'background image']);
  // A colour with no ratio (not sRGB) is the worst, so the pair is not decided.
  const named = el({...opaque, backgroundColor: 'white'}, body);
  const unmeasured = await run((x, y) => [span, y < 10 ? dark : named, body]);
  assert.deepEqual([unmeasured.bgColor, unmeasured.contrastRatio], ['white', null]);
  const unmeasuredFirst = await run((x, y) => [span, y < 10 ? named : dark, body]);
  assert.deepEqual([unmeasuredFirst.bgColor, unmeasuredFirst.contrastRatio], ['white', null]);
});

test('only the part of the box that is shown is sampled: the viewport and every ancestor clipping its overflow', async () => {
  const body = el(opaque);
  const sample = async (rect, scroller) => {
    const parent = scroller ? el({...clear, ...scroller.style}, body) : body;
    if (scroller) parent.bounds = scroller.bounds;
    const text = el({...clear, color: 'rgb(0, 0, 0)'}, parent);
    text.rects = [rect];
    page({incomplete: [contrast(node(['span']))]}, {span: text});
    globalThis.window.stack = [text, parent, body];
    const [i] = await runner.run(opts);
    return [globalThis.window.points, i.runnerExtras];
  };
  // A long string in a scrolling code block: only its visible part.
  for (const overflow of [{overflowX: 'auto'}, {overflowY: 'scroll'}, {overflowX: 'hidden'}, {overflowY: 'clip'}]) {
    const [points] = await sample({left: 0, top: 0, width: 400, height: 20}, {style: overflow, bounds: {left: 100, top: 0, right: 200, bottom: 20}});
    assert.deepEqual(points, [[150, 10], [125, 5], [175, 5], [125, 15], [175, 15]], JSON.stringify(overflow));
  }
  // An ancestor whose overflow is visible clips nothing.
  const [unclipped] = await sample({left: 0, top: 0, width: 40, height: 20}, {style: {overflowX: 'visible', overflowY: 'visible'}, bounds: {left: 10, top: 0, right: 20, bottom: 20}});
  assert.deepEqual(unclipped[0], [20, 10]);
  // The viewport clips too.
  const [viewport] = await sample({left: 900, top: 780, width: 200, height: 40});
  assert.deepEqual(viewport[0], [950, 790]);
  // A box shown nowhere cannot be read.
  for (const bounds of [{left: 500, top: 0, right: 600, bottom: 20}, {left: 0, top: 50, right: 400, bottom: 60}]) {
    const [, extras] = await sample({left: 0, top: 0, width: 400, height: 20}, {style: {overflowX: 'auto'}, bounds});
    assert.deepEqual([extras.colourSource, extras.undeterminedBy], ['undetermined', 'clipped']);
  }
});
