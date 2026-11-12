'use strict';
// The unit test of offsite.js, run by scripts/docs-offsite.test.sh under
// node:test and held to 100 % line, branch and function coverage. A fake
// browser stands in for puppeteer, except in the one test that launches the
// real Chrome named by CHROME_PATH (the test script sets it), so the launch
// path is executed as well.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {pathToFileURL} = require('node:url');
const {execFileSync} = require('node:child_process');
const offsite = require('./offsite.js');

const siteDir = fs.mkdtempSync(path.join(os.tmpdir(), 'offsite-'));
const site = pathToFileURL(siteDir).href + '/';

// browser(requests, failing): a fake browser whose page, on goto, emits a
// request for each URL of requests[name], and whose goto throws for a page
// named in failing. Records what each request was answered with.
const browser = (requests = {}, failing = []) => {
  const answered = [];
  let closed = 0;
  return {
    answered,
    closed: () => closed,
    newPage: async () => {
      const handlers = {};
      return {
        setRequestInterception: async on => assert.equal(on, true),
        on: (event, fn) => {
          handlers[event] = fn;
        },
        goto: async url => {
          const name = url.slice(site.length);
          if (failing.includes(name)) throw new Error('net::ERR_FILE_NOT_FOUND');
          for (const u of requests[name] || []) {
            handlers.request({
              url: () => u,
              continue: () => answered.push(['continue', u]),
              abort: () => answered.push(['abort', u])
            });
          }
        },
        close: async () => {}
      };
    },
    close: async () => {
      closed++;
    }
  };
};

const io = () => {
  const lines = {out: [], err: []};
  return {lines, out: s => lines.out.push(s), err: s => lines.err.push(s)};
};

test('own: the site, data:, blob: and about: URLs are the site own; anything else is not', () => {
  assert.equal(offsite.own(site + 'index.html', site), true);
  assert.equal(offsite.own(site + 'reference.html#tag', site), true);
  assert.equal(offsite.own('data:font/woff2;base64,AAAA', site), true);
  assert.equal(offsite.own('blob:file:///x', site), true);
  assert.equal(offsite.own('about:blank', site), true);
  assert.equal(offsite.own('https://fonts.googleapis.com/css?family=Roboto', site), false);
  assert.equal(offsite.own('https://fonts.gstatic.com/s/roboto.woff2', site), false);
  assert.equal(offsite.own('//cdn.example/x.js', site), false);
  assert.equal(offsite.own('file:///etc/hosts', site), false);
});

test('a site whose pages request only their own files passes and names every page', async () => {
  const b = browser({'index.html': [site + 'index.html', site + 'openapi.json', 'data:image/png;base64,AA']});
  const {lines, out, err} = io();
  const code = await offsite.check({browser: b, siteDir, pages: ['index.html', 'reference.html'], out, err});
  assert.equal(code, 0);
  assert.deepEqual(lines.err, []);
  assert.ok(lines.out.includes('docs offsite: read index.html'));
  assert.ok(lines.out.includes('docs offsite: read reference.html'));
  assert.match(lines.out.at(-1), /2 page\(s\) opened, no request left the site/);
  assert.ok(b.answered.every(([how]) => how === 'continue'));
});

test('a request to another host fails, names the page and the URL, and is aborted', async () => {
  const font = 'https://fonts.googleapis.com/css?family=Montserrat';
  const b = browser({'reference.html': [site + 'reference.html', font]});
  const {lines, out, err} = io();
  const code = await offsite.check({browser: b, siteDir, pages: ['index.html', 'reference.html'], out, err});
  assert.equal(code, 1);
  assert.ok(lines.err.includes(`FAIL: reference.html: requests ${font}`));
  assert.match(lines.err.at(-1), /1 off-site request\(s\) over 2 page\(s\)/);
  assert.deepEqual(b.answered.at(-1), ['abort', font]);
});

test('a page that does not load is a refusal, not a pass', async () => {
  const b = browser({}, ['gone.html']);
  const {lines, out, err} = io();
  const code = await offsite.check({browser: b, siteDir, pages: ['index.html', 'gone.html'], out, err});
  assert.equal(code, 2);
  assert.match(lines.err[0], /gone\.html: could not be loaded: net::ERR_FILE_NOT_FOUND/);
});

test('main refuses a missing argument and a call without pages', async () => {
  for (const argv of [[], ['chrome']]) {
    const {lines, out, err} = io();
    assert.equal(await offsite.main(argv, {out, err}), 2);
    assert.match(lines.err[0], /^usage:/);
  }
  const {lines, out, err} = io();
  assert.equal(await offsite.main(['chrome', siteDir], {out, err}), 2);
  assert.match(lines.err[0], /no page given/);
});

test('main checks with the launched browser and closes it, also on an error', async () => {
  const b = browser();
  const {out, err} = io();
  assert.equal(await offsite.main(['chrome', siteDir, 'index.html'], {out, err, launch: async () => b}), 0);
  assert.equal(b.closed(), 1);
  const broken = {...browser(), newPage: async () => Promise.reject(new Error('browser gone'))};
  let closed = false;
  broken.close = async () => {
    closed = true;
  };
  await assert.rejects(offsite.main(['chrome', siteDir, 'index.html'], {out, err, launch: async () => broken}), /browser gone/);
  assert.equal(closed, true);
});

test('main prints to the console by default', async t => {
  const logged = [];
  t.mock.method(console, 'log', s => logged.push(s));
  t.mock.method(console, 'error', s => logged.push(s));
  assert.equal(await offsite.main(['chrome', siteDir, 'index.html'], {launch: async () => browser()}), 0);
  assert.equal(await offsite.main([]), 2);
  assert.ok(logged.some(s => /no request left the site/.test(s)));
  assert.ok(logged.some(s => /^usage:/.test(s)));
});

test('the real Chrome: an own page passes, a third-party stylesheet fails and is never fetched', {timeout: 120000}, async () => {
  const chrome = process.env.CHROME_PATH;
  assert.ok(chrome, 'CHROME_PATH must name a Chrome (docs-offsite.test.sh sets it)');
  fs.writeFileSync(path.join(siteDir, 'index.html'), '<!doctype html><title>t</title><link rel="stylesheet" href="own.css"><p>x</p>');
  fs.writeFileSync(path.join(siteDir, 'own.css'), 'p{color:#000}');
  fs.writeFileSync(
    path.join(siteDir, 'leak.html'),
    '<!doctype html><title>t</title><link rel="stylesheet" href="https://fonts.googleapis.com/css?family=Roboto"><p>x</p>'
  );
  const clean = io();
  assert.equal(await offsite.main([chrome, siteDir, 'index.html'], clean), 0, clean.lines.err.join('\n'));
  const leak = io();
  assert.equal(await offsite.main([chrome, siteDir, 'index.html', 'leak.html'], leak), 1);
  assert.ok(leak.lines.err.some(s => s.startsWith('FAIL: leak.html: requests https://fonts.googleapis.com/')));
});

test('run as a command, offsite.js answers a call without arguments with its usage (exit 2)', () => {
  let status = 0;
  let stderr = '';
  try {
    execFileSync(process.execPath, [path.join(__dirname, 'offsite.js')], {stdio: 'pipe'});
  } catch (e) {
    status = e.status;
    stderr = String(e.stderr);
  }
  assert.equal(status, 2);
  assert.match(stderr, /^usage:/);
});

test('run as a command, a launch that throws is a refusal (exit 2)', () => {
  let status = 0;
  let stderr = '';
  try {
    execFileSync(process.execPath, [path.join(__dirname, 'offsite.js'), '/nonexistent/chrome', siteDir, 'index.html'], {stdio: 'pipe'});
  } catch (e) {
    status = e.status;
    stderr = String(e.stderr);
  }
  assert.equal(status, 2);
  assert.match(stderr, /^FAIL: /);
});

test.after(() => fs.rmSync(siteDir, {recursive: true, force: true}));
