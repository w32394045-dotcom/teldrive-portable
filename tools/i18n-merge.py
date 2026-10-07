# -*- coding: utf-8 -*-
"""Merge batch-2 translations into the main catalogs.

- unions main + extra
- drops keys that no longer exist in the source (stale entries from earlier runs)
- validates: no empty values, every {{placeholder}} in the key survives in the value
"""
import json
import os
import re
import sys

WS = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace"
MSG = os.path.join(WS, "src", "teldrive-2", "ui", "src", "i18n", "messages")

static = set(json.load(open(os.path.join(WS, "tools", "i18n-keys-final.json"), encoding="utf-8")))
manual = json.load(open(os.path.join(WS, "tools", "i18n-manual.json"), encoding="utf-8"))
wanted = static | {row["text"] for row in manual["manual"]} | {"Search", "Settings", "Teldrive"}

PLACEHOLDER = re.compile(r"\{\{\s*([\w.$-]+)\s*\}\}")
problems = []

for locale in ["zh-CN", "zh-TW", "ja", "ko"]:
    main_path = os.path.join(MSG, f"{locale}.json")
    extra_path = os.path.join(WS, "tools", f"i18n-extra-{locale}.json")

    catalog = json.load(open(main_path, encoding="utf-8"))
    before = len(catalog)
    if os.path.exists(extra_path):
        extra = json.load(open(extra_path, encoding="utf-8"))
        catalog.update(extra)
        print(f"{locale}: merged {len(extra)} extra keys")

    merged = {k: v for k, v in catalog.items() if k in wanted}
    dropped = before + (len(extra) if os.path.exists(extra_path) else 0) - len(merged)

    missing = sorted(wanted - set(merged))
    if missing:
        problems.append(f"{locale}: still missing {len(missing)} keys, e.g. {missing[:5]}")

    for key, value in merged.items():
        if not str(value).strip():
            problems.append(f"{locale}: empty translation for {key!r}")
            continue
        for name in set(PLACEHOLDER.findall(key)):
            if f"{{{{{name}}}}}" not in str(value) and f"{{{{ {name} }}}}" not in str(value):
                problems.append(f"{locale}: placeholder {{{{{name}}}}} lost in {key!r} -> {value!r}")

    ordered = {key: merged[key] for key in sorted(merged)}
    json.dump(ordered, open(main_path, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
    print(f"{locale}: {len(ordered)} entries (merged {len(merged)}, dropped {dropped} stale/unknown)")

if problems:
    print("\nPROBLEMS:")
    for row in problems[:40]:
        print("  ", row)
    sys.exit(1)
print("\nall catalogs consistent with the source")
