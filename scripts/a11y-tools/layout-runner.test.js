'use strict';
// The unit test of layout-runner.js, run by the docs-portal CI job with
// node:test and held to 100 % line, branch and function coverage (see
// axe-runner.test.js for the command). The run function is evaluated in the
// page by pa11y; here it runs against a fake window whose elements are
// plain objects.
const test = require('node:test');
const assert = require('node:assert/strict');
const runner = require('./layout-runner.js');

const line = '1px';
const colour = 'rgb(124, 124, 187)';
// row(kind): a table row and its field cell; kind is first, middle, last or
// single (first and last).
const row = (kind, cell = {}, before = {}) => {
  const tr = {
    nodeType: 1,
    first: kind === 'first' || kind === 'single',
    classList: {contains: c => c === 'last' && (kind === 'last' || kind === 'single')},
    matches(sel) {
      assert.equal(sel, ':first-of-type');
      return this.first;
    }
  };
  const ends = kind !== 'middle';
  const geometry = kind === 'first' ? {top: '22px', bottom: '0px'} : {top: '0px', height: '22px'};
  return {
    nodeType: 1,
    parentElement: tr,
    getAttribute: () => kind,
    style: {backgroundImage: 'none', borderLeftWidth: ends ? '0px' : line, borderLeftColor: colour, ...cell},
    pseudo: ends
      ? {content: '""', position: 'absolute', borderLeftWidth: line, borderLeftStyle: 'solid', borderLeftColor: colour, ...geometry, ...before}
      : {content: 'none'}
  };
};
const box = (role, tagName = 'DIV', parentElement = null) => ({nodeType: 1, tagName, parentElement, getAttribute: a => (a === 'role' ? role : null)});

// install(cells, landmarks): a reference page; landmarks may override main,
// content and menu, or set reference to false for a page with no Redoc root.
const install = (cells, landmarks = {}) => {
  const root = box(null);
  const main = landmarks.main === undefined ? box('main', 'DIV', root) : landmarks.main;
  const nav = box('navigation', 'DIV', root);
  const content = landmarks.content === undefined ? box(null, 'DIV', main) : landmarks.content;
  const menu = landmarks.menu === undefined ? box(null, 'DIV', nav) : landmarks.menu;
  const mains = landmarks.mains || (main ? [main] : []);
  globalThis.window = {
    document: {
      querySelector: sel =>
        ({'#redoc': landmarks.reference === false ? null : root, '.api-content': content, '.menu-content': menu})[sel],
      querySelectorAll: sel => ({'td[kind=field]': cells, 'main, [role=main]': mains})[sel]
    },
    getComputedStyle: (e, pseudo) => (pseudo ? (assert.equal(pseudo, '::before'), e.pseudo) : e.style)
  };
};
const codes = async () => (await runner.run()).map(i => `${i.code}: ${i.message}`);

test('a page that is not the reference gets no issue', async () => {
  install([], {reference: false});
  assert.deepEqual(await runner.run(), []);
});

test('a reference whose every row draws Redoc\'s line, with its landmarks, passes', async () => {
  install([row('first'), row('middle'), row('last'), row('single')]);
  assert.deepEqual(await runner.run(), []);
});

test('a reference with no field cell fails closed', async () => {
  install([]);
  assert.deepEqual(await codes(), ['tree-line: the reference shows no schema field cell; the tree-line check read nothing']);
});

test('each missing or wrong tree-line segment is an error naming its cell', async () => {
  const cases = [
    [row('first', {backgroundImage: 'linear-gradient(red, red)'}), 'still draws its tree line as a background image'],
    [row('middle', {borderLeftWidth: '0px'}), '(a middle row) has no 1 px left border'],
    [row('first', {}, {content: 'none'}), '(the first row) has no 1 px segment'],
    [row('first', {}, {content: 'normal'}), '(the first row) has no 1 px segment'],
    [row('first', {}, {position: 'static'}), '(the first row) has no 1 px segment'],
    [row('last', {}, {borderLeftWidth: '0px'}), '(the last row) has no 1 px segment'],
    [row('last', {}, {borderLeftStyle: 'dotted'}), '(the last row) has no 1 px segment'],
    [row('last', {}, {borderLeftColor: 'rgb(0, 0, 0)'}), '(the last row) has no 1 px segment'],
    [row('first', {}, {top: '0px'}), '{"top":"22px","bottom":"0px"}'],
    [row('last', {}, {height: '100%'}), '{"top":"0px","height":"22px"}'],
    // A single row is drawn as Redoc draws a last one.
    [row('single', {}, {top: '22px', bottom: '0px', height: 'auto'}), '(the last row) has no 1 px segment']
  ];
  for (const [cell, want] of cases) {
    install([row('middle'), cell]);
    const issues = await runner.run();
    assert.equal(issues.length, 1, want);
    assert.equal(issues[0].code, 'tree-line');
    assert.equal(issues[0].type, 'error');
    assert.equal(issues[0].element, cell);
    assert.match(issues[0].message, new RegExp(want.replace(/[()[\]{}.*+?^$|\\]/g, '\\$&')));
    assert.match(issues[0].message, /^the field cell "/);
  }
});

test('the content column must be the one main landmark, and the menu a navigation landmark outside it', async () => {
  const cell = [row('middle')];
  const elsewhere = box('main', 'MAIN');
  const cases = [
    [{mains: [box('main'), box('main')]}, ['landmarks: the reference has 2 main landmarks, want 1', 'landmarks: the content column is not in the main landmark']],
    [{main: null}, ['landmarks: the reference has 0 main landmarks, want 1', 'landmarks: the content column is not in the main landmark']],
    [{content: null}, ['landmarks: the content column is not in the main landmark']],
    [{content: box(null, 'DIV', elsewhere)}, ['landmarks: the content column is not in the main landmark']],
    [{menu: null}, ['landmarks: the menu is not a navigation landmark outside the main one']],
    [{menu: box(null)}, ['landmarks: the menu is not a navigation landmark outside the main one']],
    [{menu: box('navigation', 'DIV', box('main'))}, ['landmarks: the menu is not a navigation landmark outside the main one']]
  ];
  for (const [landmarks, want] of cases) {
    install(cell, landmarks);
    assert.deepEqual(await codes(), want, JSON.stringify(want));
  }
  // <main> and <nav> elements count as their roles.
  const main = box(null, 'MAIN');
  install(cell, {main, content: box(null, 'DIV', main), menu: box(null, 'DIV', box(null, 'NAV'))});
  assert.deepEqual(await runner.run(), []);
});

test('the module injects no script and names the pa11y versions it supports', () => {
  assert.deepEqual(runner.scripts, []);
  assert.equal(runner.supports, '^10.0.0');
});
