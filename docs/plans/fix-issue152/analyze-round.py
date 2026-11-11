"""issue #152 — wire reconstruction + byte counts for one capture.

Run:  python3 docs/plans/fix-issue152/analyze-round.py [raw/capture-ios.ndjson]
Prints the exact byte stream the browser wrote to the PTY, per-glyph counts,
and the full event chain of the first duplicated tap.
"""
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
VS = '\ufe0f'
path = sys.argv[1] if len(sys.argv) > 1 else os.path.join(HERE, 'raw', 'capture-ios.ndjson')
if not os.path.isabs(path):
    path = os.path.join(HERE, path)

rows = []
for line in open(path, encoding='utf8'):
    s = line.strip()
    if s.startswith('{'):
        try:
            rows.append(json.loads(s))
        except Exception:
            pass

tx = [r for r in rows if r['ev'] == 'tx' and r['chan'] in ('ws-input', 'http-input')]
wire = ''.join(r.get('text', '') for r in tx)

print('capture:', os.path.relpath(path, HERE), ' rows:', len(rows), ' input frames:', len(tx))
print('wire   :', repr(wire))
print()
seen = []
for ch in wire:
    if ord(ch) > 0x2000 and ch not in seen:
        seen.append(ch)
for ch in seen:
    print('  U+%04X %s x%d' % (ord(ch), ch, wire.count(ch)))

# first duplicated tap: base-only frame immediately followed by base+VS16
for i in range(1, len(tx)):
    prev, cur = tx[i - 1], tx[i]
    t = cur.get('text', '')
    if t and VS in t and prev.get('text', '') == t.replace(VS, ''):
        print('\nfirst duplicated tap: %r  (frames %r + %r)' % (t, prev.get('hex'), cur.get('hex')))
        m0, m1 = prev['m'], cur['m']
        print('chain:')
        for r in rows:
            if r['ev'] != 'dom' or r.get('phase') != 'capture':
                continue
            if m0 - 80 <= r['m'] <= m1 + 5:
                kind = ('keypress U+%04X' % r['charCode']) if r.get('charCode') else \
                       (r.get('inputType') or ('keydown' if r.get('keyCode') == 0 else 'keyup'))
                print('   m=%-9s %-14s key=%r dp=%s' % (r['m'], kind, r.get('key'), r.get('dp')))
        break
