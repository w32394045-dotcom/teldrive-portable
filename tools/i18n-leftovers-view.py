# -*- coding: utf-8 -*-
"""Summarise i18n leftovers so the manual pass has a short, reviewable list."""
import json
import os
import sys
from collections import Counter

WS = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace"
rows = json.load(open(os.path.join(WS, "tools", "i18n-leftovers.json"), encoding="utf-8"))

bucket = sys.argv[1] if len(sys.argv) > 1 else None
limit = int(sys.argv[2]) if len(sys.argv) > 2 else 40

print("total:", len(rows))
print(Counter((r["kind"], r["where"]) for r in rows).most_common(20))
if bucket:
    sel = [r for r in rows if r["where"] == bucket or r["kind"] == bucket]
else:
    sel = rows
print(f"\n--- showing {min(limit, len(sel))} of {len(sel)} ---")
seen = set()
shown = 0
for r in sel:
    sig = (r["kind"], r["where"], r["text"][:40])
    if sig in seen:
        continue
    seen.add(sig)
    print(f'{r["rel"]}:{r["line"]}  [{r["kind"]}/{r["where"]}]')
    print(f'    text: {r["text"]!r}')
    print(f'    ctx : {r["snippet"][:190]}')
    shown += 1
    if shown >= limit:
        break
