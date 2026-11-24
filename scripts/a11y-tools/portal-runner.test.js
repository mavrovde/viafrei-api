'use strict';
// The unit test of portal-runner.js, run by the docs-portal CI job with
// node:test and held to 100 % line, branch and function coverage (see
// axe-runner.test.js for the command). The run function is evaluated in the
// page by pa11y; here it runs against a fake window whose elements are
// plain objects.
const test = require('node:test');
const assert = require('node:assert/strict');
const runner = require('./portal-runner.js');

// el(props): a fake element; style is its computed style while it does not
// have the focus, focusStyle while it has it.
const el = (props = {}) => {
  const e = {
    tagName: 'A',
    className: 'link',
    tabIndex: 0,
    disabled: false,
    textContent: 'text',
    attrs: {},
    rects: 1,
    clientWidth: 100,
    scrollWidth: 100,
    clientHeight: 20,
    scrollHeight: 20,
    style: {visibility: 'visible', outlineStyle: 'none', outlineWidth: '0px', boxShadow: 'none', overflowX: 'visible', overflowY: 'visible'},
    focusStyle: {outlineStyle: 'solid', outlineWidth: '3px'},
    inside: [],
    closest: () => null,
    getAttribute(a) {
      return this.attrs[a] ?? null;
    },
    getClientRects() {
      return {length: this.rects};
    },
    querySelector(sel) {
      return this.inside.find(x => x.matches(sel)) || null;
    },
    matches: () => false,
    focus(opts) {
      const w = globalThis.window;
      w.focusOptions.push(opts);
      if (this.focusable !== false) w.document.activeElement = this;
      if (this.movesUrl) w.location.href = 'file:///page.html#moved';
    },
    ...props
  };
  return e;
};

const skipTarget = () => {
  const main = el({tagName: 'MAIN'});
  return el({tagName: 'DIV', closest: sel => (sel === 'main, [role=main]' ? main : null)});
};

// install(page): a page whose focusables, headings, tables and text
// elements are page's; skip is the skip link (default a good one).
const install = (page = {}) => {
  const target = page.target === undefined ? skipTarget() : page.target;
  const skip = page.skip === undefined ? el({textContent: ' Skip  to content ', attrs: {href: '#main'}}) : page.skip;
  const focusables = [...(skip ? [skip] : []), ...(page.focusables || [])];
  const removed = [];
  globalThis.window = {
    innerWidth: page.width || 1280,
    focusOptions: [],
    location: {href: 'file:///page.html'},
    history: {
      state: {s: 1},
      replaceState(state, title, url) {
        globalThis.window.replaced = [state, title, url];
        globalThis.window.location.href = url;
      }
    },
    document: {
      activeElement: page.active === undefined ? null : page.active,
      documentElement: {clientWidth: page.width || 1280},
      scrollingElement: {scrollWidth: page.scrollWidth || page.width || 1280},
      getElementById: id => (id === 'main' ? target : null),
      querySelectorAll(sel) {
        if (sel.startsWith('a[href]')) return focusables;
        if (sel.startsWith('h1')) return page.headings || [];
        if (sel === 'table') return page.tables || [];
        throw new Error(`unexpected selector ${sel}`);
      },
      body: {
        querySelectorAll: sel => {
          assert.equal(sel, '*');
          return page.texts || [];
        }
      },
      head: {
        appendChild(s) {
          globalThis.window.spaced = s.textContent;
        }
      },
      createElement: tag => ({
        tag,
        remove() {
          removed.push(tag);
        }
      })
    },
    getComputedStyle: e => (globalThis.window.document.activeElement === e ? {...e.style, ...e.focusStyle} : e.style)
  };
  return removed;
};
const codes = async () => (await runner.run()).map(i => `${i.code}: ${i.message}`);

test('a page with a skip link to its main landmark and visible focus has no issue', async () => {
  const removed = install();
  assert.deepEqual(await codes(), []);
  assert.deepEqual(globalThis.window.focusOptions, [{preventScroll: true}], 'focusing never scrolls');
  assert.match(globalThis.window.spaced, /letter-spacing:0\.12em/);
  assert.deepEqual(removed, ['style'], 'the text spacing is removed after');
});

test('the first stop must be a "Skip to content" link to the main landmark', async () => {
  const cases = {
    'no focusable': {skip: null},
    'a button first': {skip: el({tagName: 'BUTTON', textContent: 'Skip to content', attrs: {href: '#main'}})},
    'another text': {skip: el({textContent: 'Skip', attrs: {href: '#main'}})},
    'no href': {skip: el({textContent: 'Skip to content'})},
    'not a fragment': {skip: el({textContent: 'Skip to content', attrs: {href: 'main.html'}})},
    'an unknown id': {target: null},
    'a target outside main': {target: el({tagName: 'DIV'})}
  };
  for (const [name, page] of Object.entries(cases)) {
    install(page);
    assert.deepEqual((await codes()).filter(c => c.startsWith('skip-link')).length, 1, name);
  }
});

test('the keyboard reaches only rendered, visible, enabled elements in the tab order', async () => {
  const good = el({textContent: 'Skip to content', attrs: {href: '#main'}});
  install({
    skip: null,
    focusables: [
      el({tabIndex: -1, textContent: 'Skip to content'}),
      el({disabled: true}),
      el({rects: 0}),
      el({style: {...el().style, visibility: 'hidden'}}),
      good
    ]
  });
  assert.deepEqual(await codes(), []);
});

test('each kind of focusable must show an outline of 2 px or more or a box shadow; one of each kind is focused', async () => {
  const thin = el({tagName: 'PRE', className: 'code', focusStyle: {outlineStyle: 'auto', outlineWidth: '1px'}});
  const none = el({tagName: 'BUTTON', className: 'copy x', focusStyle: {}});
  const twin = el({tagName: 'BUTTON', className: 'copy y', focusStyle: {}});
  const shadow = el({tagName: 'INPUT', className: 'search', focusStyle: {boxShadow: '0 0 0 5px #fff'}});
  const refusing = el({tagName: 'DIV', className: 'menu', focusable: false});
  const plain = el({tagName: 'SPAN', className: {baseVal: 'svg'}, focusStyle: {outlineStyle: 'solid', outlineWidth: '2px'}});
  install({focusables: [thin, none, twin, shadow, refusing, plain]});
  assert.deepEqual(await codes(), [
    'focus-visible: PRE code shows no outline of 2 px or more and no box shadow while it has the focus',
    'focus-visible: BUTTON copy shows no outline of 2 px or more and no box shadow while it has the focus',
    'focus-visible: DIV menu shows no outline of 2 px or more and no box shadow while it has the focus'
  ]);
  assert.equal(globalThis.window.focusOptions.length, 6, 'the skip link and five kinds, not the twin');
});

test('the focus goes back where it was, and the URL too when focusing moved it', async () => {
  const active = el({tagName: 'BUTTON', className: 'was'});
  install({active, focusables: [el({tagName: 'SELECT', className: 's', movesUrl: true})]});
  assert.deepEqual(await codes(), []);
  assert.equal(globalThis.window.document.activeElement, active);
  assert.deepEqual(globalThis.window.replaced, [{s: 1}, '', 'file:///page.html']);
  install({active: {}});
  assert.deepEqual(await codes(), [], 'an active element that cannot take the focus is left');
  assert.equal(globalThis.window.replaced, undefined, 'an unmoved URL is not rewritten');
});

test('a heading holding a labelled element must be named by its own text', async () => {
  const anchor = el({matches: sel => sel === '[aria-label]'});
  const named = el({tagName: 'H2', textContent: ' The  key ', attrs: {'aria-label': 'The key'}, inside: [anchor]});
  const unnamed = el({tagName: 'H2', textContent: 'Liveness', inside: [anchor]});
  const wrong = el({tagName: 'H2', textContent: 'Liveness', attrs: {'aria-label': 'tag/meta'}, inside: [anchor]});
  const plain = el({tagName: 'H3', textContent: 'Responses'});
  install({headings: [named, unnamed, wrong, plain]});
  assert.deepEqual(await codes(), [
    'heading-name: the heading "Liveness" is named by the label of an element in it, not by its own text',
    'heading-name: the heading "Liveness" is named by the label of an element in it, not by its own text'
  ]);
});

test('every rendered table must have a header cell', async () => {
  const headed = el({tagName: 'TABLE', inside: [el({matches: sel => sel.startsWith('th')})]});
  const bare = el({tagName: 'TABLE'});
  const hidden = el({tagName: 'TABLE', rects: 0});
  install({tables: [headed, bare, hidden]});
  assert.deepEqual(await codes(), ['table-headers: a table has no header cell']);
});

test('text cut off by a hidden or clipped overflow fails, at the page\'s spacing and at the WCAG spacing', async () => {
  const style = (x, y) => ({...el().style, overflowX: x, overflowY: y});
  const texts = [
    el({textContent: '/v1/motorways/{road}/parking-options', style: style('hidden', 'visible'), scrollWidth: 150}),
    el({textContent: 'clipped', style: style('visible', 'clip'), scrollHeight: 40}),
    el({textContent: 'fits', style: style('hidden', 'hidden')}),
    el({textContent: 'scrolls', style: style('auto', 'auto'), scrollWidth: 400, scrollHeight: 400}),
    el({textContent: 'Field', style: style('hidden', 'hidden'), clientWidth: 1, scrollWidth: 40}),
    el({textContent: '  ', style: style('hidden', 'hidden'), scrollWidth: 400}),
    el({textContent: 'gone', style: style('hidden', 'hidden'), scrollWidth: 400, rects: 0})
  ];
  install({texts});
  assert.deepEqual(await codes(), [
    'clipped-text: text is cut off at the page\'s text spacing: "/v1/motorways/{road}/parking-options"',
    'clipped-text: text is cut off at the page\'s text spacing: "clipped"',
    'clipped-text: text is cut off at the WCAG 1.4.12 text spacing: "/v1/motorways/{road}/parking-options"',
    'clipped-text: text is cut off at the WCAG 1.4.12 text spacing: "clipped"'
  ]);
});

test('the text spacing is removed even when a read throws', async () => {
  const removed = install({texts: [el({textContent: 'x', style: null})]});
  globalThis.window.getComputedStyle = () => {
    if (globalThis.window.spaced) throw new Error('no style');
    return el().style;
  };
  await assert.rejects(runner.run(), /no style/);
  assert.deepEqual(removed, ['style']);
});

test('at 320 px the page must not scroll sideways, and the text spacing is left to the full width', async () => {
  install({width: 320, scrollWidth: 358});
  assert.deepEqual(await codes(), ['reflow: at 320 px wide the page scrolls sideways (358 px wide)']);
  assert.equal(globalThis.window.spaced, undefined);
  install({width: 320, scrollWidth: 321});
  assert.deepEqual(await codes(), [], 'a pixel of rounding is no scroll');
  install({width: 1280, scrollWidth: 2000});
  assert.deepEqual(await codes(), [], 'at full width the page width is not judged');
});
