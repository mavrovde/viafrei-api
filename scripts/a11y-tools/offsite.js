'use strict';
// offsite.js CHROME SITE_DIR PAGE...
//
// The privacy check of the built documentation portal (#81): every page is
// opened in headless Chrome, and every request the page makes is held
// against the site's own origin. A request for anything but a file of the
// site (or a data:, blob: or about: URL, which leave the browser for no
// host) is a finding: a web font, a stylesheet, a script or an image from a
// third party hands that party the visitor's address before any consent.
// Off-site requests are aborted, so the check itself contacts no host.
//
// Run by scripts/docs-offsite.sh, which installs the pinned toolchain and
// finds Chrome. Prints every page it read; fails closed on no page and on a
// page that did not load.
// Exit 0 clean, 1 findings, 2 refused (usage, no page, a page not loaded).
const path = require('node:path');
const {pathToFileURL} = require('node:url');

const timeout = 60000;

// own(url, site): whether url names the site itself (site is the file: URL
// of the site directory, with a trailing slash) or leaves the browser for no
// host at all.
const own = (url, site) => /^(?:data|blob|about):/i.test(url) || url.startsWith(site);

// launch(chrome): the browser, from the toolchain's puppeteer.
const launch = chrome =>
  require('puppeteer').launch({
    executablePath: chrome,
    headless: true,
    args: ['--no-sandbox', '--disable-dev-shm-usage']
  });

// check({browser, siteDir, pages, out, err}): open every page, record every
// off-site request. Returns the exit code.
async function check({browser, siteDir, pages, out, err}) {
  const site = pathToFileURL(path.resolve(siteDir)).href.replace(/\/?$/, '/');
  const findings = [];
  const refusals = [];
  for (const name of pages) {
    const page = await browser.newPage();
    await page.setRequestInterception(true);
    page.on('request', req => {
      const url = req.url();
      if (own(url, site)) return req.continue();
      findings.push(`${name}: requests ${url}`);
      return req.abort();
    });
    try {
      await page.goto(site + name, {waitUntil: 'networkidle0', timeout});
      out(`docs offsite: read ${name}`);
    } catch (e) {
      refusals.push(`${name}: could not be loaded: ${e.message}`);
    }
    await page.close();
  }
  for (const r of refusals) err(`FAIL: ${r}`);
  if (refusals.length) return 2;
  if (findings.length) {
    for (const f of findings) err(`FAIL: ${f}`);
    err(`FAIL: ${findings.length} off-site request(s) over ${pages.length} page(s)`);
    return 1;
  }
  out(`docs offsite: ${pages.length} page(s) opened, no request left the site`);
  return 0;
}

// main(argv, io): the command line. io.launch replaces the browser (tests).
async function main(argv, io = {}) {
  const out = io.out || (s => console.log(s));
  const err = io.err || (s => console.error(s));
  const [chrome, siteDir, ...pages] = argv;
  if (!chrome || !siteDir) {
    err('usage: offsite.js CHROME SITE_DIR PAGE...');
    return 2;
  }
  if (!pages.length) {
    err(`FAIL: no page given for ${siteDir}: refusing to report a clean site`);
    return 2;
  }
  const browser = await (io.launch || launch)(chrome);
  try {
    return await check({browser, siteDir, pages, out, err});
  } finally {
    await browser.close();
  }
}

module.exports = {own, launch, check, main};

if (require.main === module) {
  main(process.argv.slice(2)).then(
    code => {
      process.exitCode = code;
    },
    e => {
      console.error(`FAIL: ${e.message}`);
      process.exitCode = 2;
    }
  );
}
