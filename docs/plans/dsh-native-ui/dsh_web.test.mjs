// JS unit tests for dsh_web.js _tokenizeIframeSrc
// (REVIEW-2026-08-16-uncommitted.md #4: pin the three branches —
// server-tokenized src / address-bar fallback / warn).
//
// Run: node --test docs/plans/dsh-native-ui/dsh_web.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const SRC_PATH = new URL('../../../internal/ui/static/kinds/dsh_web.js', import.meta.url);
const source = readFileSync(SRC_PATH, 'utf8');

// Load the renderer script with a stubbed window; capture the
// registered renderer singleton. The file is a plain script (no
// exports) — eval runs it in its own scope, which is fine because the
// only thing we need back is the registered renderer. The stub also
// provides isRemoteAccess, mirroring the framework.js helper contract
// (loopback = localhost / 127.0.0.1 / ::1 / [::1]).
function loadRenderer(location) {
  let renderer = null;
  globalThis.window = {
    location,
    isRemoteAccess() {
      const h = location.hostname;
      return !(h === 'localhost' || h === '127.0.0.1' || h === '::1' || h === '[::1]');
    },
    registerRenderer(kind, r) {
      if (kind === 'dsh-web') renderer = r;
    },
  };
  (0, eval)(source);
  assert.ok(renderer, 'dsh-web renderer did not register');
  return renderer;
}

test('server-tokenized src is accepted as-is (no warning, no double-append)', () => {
  const renderer = loadRenderer({ hostname: '192.168.1.5', search: '' });
  const src = 'https://192.168.1.5:40002/?token=sekret';
  assert.equal(renderer._tokenizeIframeSrc(src), src);
  assert.equal(renderer._remoteAuthMissing, false);
});

test('address-bar token is appended when the src lacks one', () => {
  const renderer = loadRenderer({ hostname: '192.168.1.5', search: '?token=addrbar' });
  const out = renderer._tokenizeIframeSrc('https://192.168.1.5:40002/');
  assert.equal(out, 'https://192.168.1.5:40002/?token=addrbar');
  assert.equal(renderer._remoteAuthMissing, false);
});

test('remote page with token-less src and no address-bar token warns', () => {
  const renderer = loadRenderer({ hostname: '192.168.1.5', search: '' });
  const src = 'https://192.168.1.5:40002/';
  assert.equal(renderer._tokenizeIframeSrc(src), src);
  assert.equal(renderer._remoteAuthMissing, true);
});

test('local pages never warn and never append tokens', () => {
  for (const hostname of ['localhost', '127.0.0.1', '::1']) {
    const renderer = loadRenderer({ hostname, search: '?token=addrbar' });
    const src = 'http://127.0.0.1:40002/';
    assert.equal(renderer._tokenizeIframeSrc(src), src);
    assert.equal(renderer._remoteAuthMissing, false);
  }
});
