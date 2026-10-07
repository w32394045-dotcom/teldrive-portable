# -*- coding: utf-8 -*-
"""Validate message catalogs against the extracted key list and print leftovers."""
import json
import os
import sys

WS = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace"
MSG = os.path.join(WS, "src", "teldrive-2", "ui", "src", "i18n", "messages")
LOCALES = ["zh-CN", "zh-TW", "ja", "ko"]

keys_now = set(json.load(open(os.path.join(WS, "tools", "i18n-keys.json"), encoding="utf-8")))
print("current extracted keys:", len(keys_now))

all_missing = {}
for name in LOCALES:
    path = os.path.join(MSG, name + ".json")
    try:
        data = json.load(open(path, encoding="utf-8"))
    except Exception as exc:  # noqa: BLE001
        print(name, "-> unreadable:", exc)
        all_missing[name] = sorted(keys_now)
        continue
    have = set(data)
    missing = sorted(keys_now - have)
    extra = sorted(have - keys_now)
    empty = [k for k, v in data.items() if not str(v).strip()]
    all_missing[name] = missing
    print(
        f"{name}: entries={len(have):4d}  missing={len(missing):3d}  extra={len(extra):3d}  empty={len(empty):3d}"
    )
    if missing:
        print("    missing:", json.dumps(missing[:10], ensure_ascii=False))
    if empty:
        print("    empty  :", json.dumps(empty[:10], ensure_ascii=False))

union = sorted(set().union(*all_missing.values())) if all_missing else []
print("\nmissing across all catalogs (union):", len(union))
out = os.path.join(WS, "tools", "i18n-missing.json")
json.dump({name: all_missing.get(name, []) for name in LOCALES}, open(out, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
print("wrote", out)

print("\n=== module-level leftovers (need render-time t()) ===")
manual = json.load(open(os.path.join(WS, "tools", "i18n-manual.json"), encoding="utf-8"))
for row in manual["manual"]:
    print(f"  {row['rel']}:{row['line']}  {json.dumps(row['text'], ensure_ascii=False)}")
