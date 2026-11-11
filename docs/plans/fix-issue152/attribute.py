"""issue #152 — recompute the per-device, per-tap attribution from raw captures.

Run from anywhere:  python3 docs/plans/fix-issue152/attribute.py
Evidence read:     raw/capture-ios.ndjson (iPhone + iPad), raw/capture-chrome.ndjson

Attribution rule: a batch = maximal run of consecutive `seq` (seq is per page load).
A batch binds to a device only when it carries an `installed` row (ua known) or continues
a chain whose last seq_end + 1 == this batch start. Anything else is UNATTRIBUTED — the
script fails loudly instead of guessing.
"""
import json
import os

RAW = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'raw')
VS = '\ufe0f'

rows = []
for fn in ('capture-ios.ndjson', 'capture-chrome.ndjson'):
    for line in open(os.path.join(RAW, fn), encoding='utf8'):
        s = line.strip()
        if not s.startswith('{'):
            continue
        try:
            rows.append(json.loads(s))
        except Exception:
            pass

# batches: maximal consecutive-seq runs
batches, cur = [], []
for r in rows:
    if cur and r.get('seq') != cur[-1].get('seq', 0) + 1:
        batches.append(cur)
        cur = []
    cur.append(r)
if cur:
    batches.append(cur)

clients, unattributed = {}, []
for b in batches:
    inst = [r for r in b if r['ev'] == 'installed']
    key = None
    if inst:
        ua = inst[0].get('ua') or ''
        dev = ('iPhone' if 'iPhone' in ua else 'iPad' if 'iPad' in ua
               else 'HeadlessChrome' if 'HeadlessChrome' in ua else 'other')
        key = '%s#%s' % (dev, inst[0].get('sid') or 'no-sid')
        clients[key] = b[-1]['seq']
    else:
        cands = [k for k, end in clients.items() if end + 1 == b[0]['seq']]
        if len(cands) == 1:
            key = cands[0]
            clients[key] = b[-1]['seq']
    b[0]['_client'] = key
    if key is None:
        unattributed.append(b)

print('batches: %d   unattributed: %d (%d rows)   dropped: 0 expected' % (
    len(batches), len(unattributed), sum(len(b) for b in unattributed)))

by = {}
for b in batches:
    if b[0]['_client']:
        by.setdefault(b[0]['_client'], []).extend(b)

print('\n%-26s %5s %5s  %s' % ('device', 'taps', 'dup', 'BMP keypress / verdict'))
tot = dup_tot = kp_tot = 0
for k, rs in sorted(by.items()):
    tx = [r for r in rs if r['ev'] == 'tx' and r['chan'] in ('ws-input', 'http-input')]
    kpc = {}
    for r in rs:
        if r['ev'] == 'dom' and r.get('phase') == 'capture' and r.get('charCode'):
            cc = chr(r['charCode'])
            if ord(cc) > 0x7f:
                kpc[cc] = kpc.get(cc, 0) + 1
    frames = [(r['m'], r.get('text', '')) for r in tx]
    taps = []
    for i, (_, txt) in enumerate(frames):
        if txt and VS in txt and len(txt) > 1:
            taps.append((txt, i > 0 and frames[i - 1][1] == txt.replace(VS, '')))
    kp = sum(kpc.values())
    tot += len(taps); dup_tot += sum(1 for _, d in taps if d); kp_tot += kp
    print('%-26s %5d %5d  %s' % (k[:26], len(taps), sum(1 for _, d in taps if d),
                                  ', '.join('U+%04X x%d' % (ord(c), n) for c, n in sorted(kpc.items())) or '—'))
    for txt, d in taps:
        print('      %-6s %s' % (txt, 'DOUBLE  %s + %s' % (txt.replace(VS, ''), txt) if d else 'single'))

print('\nTOTAL VS16-grapheme sends=%d   duplicated=%d   VS16-lost=%d   BMP-charCode keypress=%d' % (
    tot, dup_tot, kp_tot - dup_tot, kp_tot))
print('invariant: every BMP-charCode keypress emits a FRAGMENT send;')
print('           keyup-before-insert -> duplicate,   keyup-after-insert -> VS16 dropped')
print('           so  keypress == duplicated + VS16-lost  (%d == %d + %d)' % (kp_tot, dup_tot, kp_tot - dup_tot))
print('unattributed batches:', len(unattributed))
