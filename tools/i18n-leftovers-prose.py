# -*- coding: utf-8 -*-
"""Count leftovers that are genuinely user-visible prose (drop CSS/tech noise)."""
import json
import os
import re

WS = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace"
rows = json.load(open(os.path.join(WS, "tools", "i18n-leftovers.json"), encoding="utf-8"))

CODEY = re.compile(r"^(?:[a-z0-9:@/\[\]()%#._\-\s]|(?:sm|md|lg|xl|2xl|4xl):)+$")


def is_prose(row):
    text = row["text"].strip()
    if len(text) < 2:
        return False
    if not re.search(r"[A-Za-z]{3,}", text):
        return False
    if CODEY.match(text) and ("-" in text or "/" in text or "@" in text):
        return False
    if text.startswith("http") or text.startswith("/api"):
        return False
    if re.match(r"^[\w.-]+@[\w.-]+$", text):
        return False
    if row["where"].startswith("key:"):
        return False
    return True


prose = [r for r in rows if is_prose(r)]
print("total leftovers:", len(rows), " prose-looking:", len(prose))
by_kind = {}
for row in prose:
    by_kind[row["kind"]] = by_kind.get(row["kind"], 0) + 1
print("by kind:", json.dumps(by_kind))

print("\n-- prose leftovers --")
seen = set()
for row in prose:
    key = row["text"]
    if key in seen:
        continue
    seen.add(key)
    print(f'{row["rel"]}:{row["line"]} [{row["kind"]}/{row["where"]}] {key!r}')

json.dump(sorted(seen), open(os.path.join(WS, "tools", "i18n-batch3-keys.json"), "w", encoding="utf-8"),
          ensure_ascii=False, indent=1)
print("\nwrote tools/i18n-batch3-keys.json with", len(seen), "unique strings")
