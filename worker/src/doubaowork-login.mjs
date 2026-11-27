// doubaowork-login.mjs
// Short-lived Playwright helper that obtains a Doubao Work web-session cookie.
//
// It uses a PERSISTENT Chrome profile (userDataDir) so a successful login is
// reused on later runs: the first run opens a visible window and waits for the
// operator to scan the QR / sign in; later runs try headless first and only
// fall back to a visible window if the session cookie is gone.
//
// Output (stdout, single JSON object):
//   {"ok":true,"cookies":[{"name":...,"value":...,"domain":...,"path":...,
//     "expires":...,"secure":...,"httpOnly":...}, ...]}
//   {"ok":false,"error":"..."}
//
// This script NEVER performs captcha solving, webdriver masking, device
// fingerprint spoofing, or any other anti-bot circumvention. It only drives a
// real browser and reads cookies the browser legitimately stores after the
// operator signs in. It exits immediately after exporting; chat traffic is
// handled in-process by the Go adapter, not by this process.

import { existsSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { chromium } from 'playwright-core';

function parseArgs(argv) {
  const args = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--user-data-dir') args.userDataDir = argv[++i];
    else if (a === '--headless') args.headless = argv[++i] === '1';
    else if (a === '--timeout') args.timeout = Number(argv[++i]);
    else if (a === '--chrome') args.chrome = argv[++i];
    else if (a === '--clear-cookies') args.clearCookies = argv[++i] === '1';
    else if (a === '--url') args.url = argv[++i];
  }
  if (!args.userDataDir) throw new Error('--user-data-dir is required');
  if (args.timeout == null || !Number.isFinite(args.timeout)) args.timeout = 180000;
  if (!args.url) args.url = 'https://www.doubao.com/chat/';
  return args;
}

function emit(obj) {
  process.stdout.write(JSON.stringify(obj) + '\n');
}

export function isDoubaoSession(c) {
  return (c.name === 'sessionid' || c.name === 'sessionid_ss') &&
    !!c.value && !!(c.domain || '').includes('doubao.com');
}

export function normalizeCookies(all) {
  return all
    .filter((c) => (c.domain || '').includes('doubao.com'))
    .map((c) => ({
      name: c.name,
      value: c.value,
      domain: c.domain,
      path: c.path || '/',
      expires: c.expires && c.expires > 0 ? Math.floor(c.expires) : 0,
      secure: !!c.secure,
      http_only: !!c.httpOnly,
    }));
}

// Keep only values the browser actually produced. Missing fields stay empty;
// the Go side never fabricates a fingerprint or device identity.
export function normalizeWebContext(raw = {}) {
  const text = (value) => (typeof value === 'string' ? value.trim() : '');
  return {
    web_id: text(raw.web_id),
    device_id: text(raw.device_id),
    client_version: text(raw.client_version),
    chromium_version: text(raw.chromium_version),
    channel: text(raw.channel),
    user_agent: text(raw.user_agent),
    client_profile: 'web',
  };
}

async function readWebContext(page) {
  const raw = await page.evaluate(() => {
    const read = (key) => {
      try {
        return localStorage.getItem(key) || '';
      } catch {
        return '';
      }
    };
    let device = {};
    try {
      device = JSON.parse(read('client_device_info') || '{}');
    } catch {
      device = {};
    }
    return {
      web_id: read('samantha_web_web_id') || read('web_id'),
      device_id: device.deviceId || device.device_id || '',
      client_version: device.clientVersion || device.client_version || '',
      chromium_version: device.chromiumVersion || device.chromium_version || '',
      channel: device.appChannel || device.channel || '',
      user_agent: navigator.userAgent || '',
    };
  }).catch(() => ({}));
  return normalizeWebContext(raw);
}

async function hasSession(context) {
  const cookies = await context.cookies();
  return cookies.some(isDoubaoSession);
}

async function waitForLogin(context, timeoutMs, onProgress) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await hasSession(context)) return true;
    onProgress();
    await new Promise((r) => setTimeout(r, 1500));
  }
  return hasSession(context);
}

function launchOptions(args) {
  const opts = {
    headless: !!args.headless,
    args: ['--no-first-run', '--no-default-browser-check'],
  };
  const executable = findChrome(args.chrome);
  if (executable) opts.executablePath = executable;
  return opts;
}

// findChrome prefers an explicit path, then DOUBAOWORK_CHROME/CHROME_PATH, then
// the standard system Chrome/Edge locations. playwright-core does not download
// a browser, so pointing at the browser the operator already has avoids a
// multi-hundred-megabyte install and works on a fresh checkout.
export function chromeCandidates() {
  if (process.platform === 'win32') {
    const roots = [
      process.env.PROGRAMFILES,
      process.env['PROGRAMFILES(X86)'],
      process.env.ProgramW6432,
      process.env.LOCALAPPDATA,
    ].filter(Boolean);
    const out = [];
    for (const root of roots) {
      out.push(join(root, 'Google', 'Chrome', 'Application', 'chrome.exe'));
      out.push(join(root, 'Microsoft', 'Edge', 'Application', 'msedge.exe'));
    }
    return out;
  }
  if (process.platform === 'darwin') {
    return [
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
      '/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge',
      '/Applications/Chromium.app/Contents/MacOS/Chromium',
    ];
  }
  return [
    '/usr/bin/google-chrome',
    '/usr/bin/google-chrome-stable',
    '/usr/bin/chromium',
    '/usr/bin/chromium-browser',
    '/snap/bin/chromium',
    '/usr/bin/microsoft-edge',
  ];
}

export function findChrome(explicit) {
  const candidates = [];
  if (explicit) candidates.push(explicit);
  if (process.env.DOUBAOWORK_CHROME) candidates.push(process.env.DOUBAOWORK_CHROME);
  if (process.env.CHROME_PATH) candidates.push(process.env.CHROME_PATH);
  candidates.push(...chromeCandidates());
  for (const candidate of candidates) {
    if (candidate && existsSync(candidate)) return candidate;
  }
  return '';
}

async function run() {
  const args = parseArgs(process.argv.slice(2));
  const deadline = Date.now() + args.timeout;
  let context;
  try {
    context = await chromium.launchPersistentContext(args.userDataDir, launchOptions(args));
    // A profile can outlive its sessionid. The Go side verifies the exported
    // Cookie; when it is stale it asks this attempt to clear the old session so
    // the visible window starts at a real login instead of looking signed in.
    if (args.clearCookies) await context.clearCookies();
    const page = context.pages()[0] || (await context.newPage());
    const gotoTimeout = Math.max(5000, Math.min(30000, deadline - Date.now()));
    await page.goto(args.url, { waitUntil: 'domcontentloaded', timeout: gotoTimeout }).catch(() => {});

    const remaining = deadline - Date.now();
    const ok = remaining > 0 ? await waitForLogin(context, remaining, () => {}) : await hasSession(context);
    if (!ok) {
      emit({ ok: false, error: 'login_timeout', message: 'no sessionid cookie within timeout' });
      return;
    }

    const all = await context.cookies();
    const webContext = await readWebContext(page);
    emit({
      ok: true,
      cookies: normalizeCookies(all),
      client_info: webContext,
      client_version: webContext.client_version,
    });
  } catch (err) {
    emit({
      ok: false,
      error: 'launch_failed',
      message: `${String(err && err.message ? err.message : err)}；若本机未安装 Chrome/Edge，请设置 DOUBAOWORK_CHROME 指向浏览器可执行文件`,
    });
  } finally {
    if (context) await context.close().catch(() => {});
  }
}

// Only drive a browser when executed directly, not when imported by tests.
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  run();
}
