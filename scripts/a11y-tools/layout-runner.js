'use strict';
// A pa11y runner for scripts/docs-a11y.sh beside axe-runner.js: it checks
// that the repairs scripts/docs-build.sh makes to the API reference render
// as intended, which no axe rule can see. Each failure is an "error" issue,
// which docs-a11y.sh fails on every page, reference.html included.
//
// - tree-line: the schema tables' tree lines. Redoc draws the first and the
//   last row's segment as a 1 px background image on the property-name cell
//   (td[kind=field]), which leaves the name's contrast undecidable; the
//   build removes the image and draws the same segment as a ::before border
//   of the cell's own line colour. Every field cell must carry no
//   background image; a middle row's line is the cell's own 1 px left
//   border; the first row's is a ::before from 22 px to the bottom, the last
//   row's (and a single row's) from the top to 22 px, as Redoc draws them,
//   in the colour of the cell's border.
// - landmarks: the content column (.api-content) is the page's one main
//   landmark, and the menu (.menu-content) is a navigation landmark outside
//   it.
//
// The page is the reference when it holds Redoc's root (#redoc); a
// reference without a field cell, a menu or a content column is a failure,
// never a pass. Other pages get no issue from this runner.
//
// The run function is stringified by pa11y and evaluated in the page, so it
// may use nothing from this module's scope. scripts/a11y-tools/
// layout-runner.test.js runs it under node:test against a fake window.

const run = async () => {
  const doc = window.document;
  if (!doc.querySelector('#redoc')) return [];
  const issues = [];
  const fail = (code, element, message) => issues.push({code, type: 'error', element, message, runnerExtras: {}});

  // The fault of one field cell's tree line, or null.
  const treeLine = td => {
    const tr = td.parentElement;
    const first = tr.matches(':first-of-type');
    const last = tr.classList.contains('last');
    const cell = window.getComputedStyle(td);
    const where = `the field cell "${td.getAttribute('title')}"`;
    if (cell.backgroundImage !== 'none') return `${where} still draws its tree line as a background image`;
    if (!first && !last) return cell.borderLeftWidth === '1px' ? null : `${where} (a middle row) has no 1 px left border`;
    const line = window.getComputedStyle(td, '::before');
    const want = last ? {top: '0px', height: '22px'} : {top: '22px', bottom: '0px'};
    const drawn = !['none', 'normal'].includes(line.content) && line.position === 'absolute' &&
      line.borderLeftWidth === '1px' && line.borderLeftStyle === 'solid' &&
      line.borderLeftColor === cell.borderLeftColor &&
      Object.entries(want).every(([k, v]) => line[k] === v);
    return drawn ? null : `${where} (the ${last ? 'last' : 'first'} row) has no 1 px segment ${JSON.stringify(want)} in its line colour`;
  };
  const cells = [...doc.querySelectorAll('td[kind=field]')];
  if (!cells.length) fail('tree-line', null, 'the reference shows no schema field cell; the tree-line check read nothing');
  for (const td of cells) {
    const fault = treeLine(td);
    if (fault) fail('tree-line', td, fault);
  }

  const landmark = (e, role, tag) => {
    for (let x = e; x?.nodeType === 1; x = x.parentElement) {
      if (x.getAttribute('role') === role || x.tagName === tag) return x;
    }
    return null;
  };
  const mains = doc.querySelectorAll('main, [role=main]');
  const content = doc.querySelector('.api-content');
  const menu = doc.querySelector('.menu-content');
  if (mains.length !== 1) fail('landmarks', null, `the reference has ${mains.length} main landmarks, want 1`);
  if (!content || landmark(content, 'main', 'MAIN') !== mains[0]) fail('landmarks', content, 'the content column is not in the main landmark');
  if (!menu || !landmark(menu, 'navigation', 'NAV') || landmark(menu, 'main', 'MAIN')) {
    fail('landmarks', menu, 'the menu is not a navigation landmark outside the main one');
  }
  return issues;
};

module.exports = {run, scripts: [], supports: '^10.0.0'};
