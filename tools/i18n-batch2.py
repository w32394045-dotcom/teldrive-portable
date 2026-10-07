# -*- coding: utf-8 -*-
"""Build the pass-2 translation batch: static keys + keys that only exist as
render-time lookups (module-level label tables reached via t(item.label))."""
import json
import os

WS = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace"
MSG = os.path.join(WS, "src", "teldrive-2", "ui", "src", "i18n", "messages")

static = set(json.load(open(os.path.join(WS, "tools", "i18n-keys-final.json"), encoding="utf-8")))
manual = json.load(open(os.path.join(WS, "tools", "i18n-manual.json"), encoding="utf-8"))
dynamic = {row["text"] for row in manual["manual"]}
# getPageTitle() returns these literals, rendered through t(title)
dynamic |= {"Search", "Settings", "Teldrive"}

wanted = static | dynamic
print("static keys:", len(static))
print("dynamic keys:", len(dynamic))
print("union:", len(wanted))

for locale in ["zh-CN", "zh-TW", "ja", "ko"]:
    data = json.load(open(os.path.join(MSG, f"{locale}.json"), encoding="utf-8"))
    have = set(data)
    missing = sorted(wanted - have)
    # Drop stale entries left over from the first (superseded) key list.
    stale = sorted(have - wanted)
    out = os.path.join(WS, "tools", f"i18n-batch2-{locale}.json")
    json.dump(missing, open(out, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
    print(f"{locale}: have={len(have)} missing={len(missing)} stale={len(stale)} -> {os.path.basename(out)}")
    json.dump(stale, open(os.path.join(WS, "tools", f"i18n-stale-{locale}.json"), "w", encoding="utf-8"),
              ensure_ascii=False, indent=1)

print("\nfirst 12 missing (zh-CN):")
for key in json.load(open(os.path.join(WS, "tools", "i18n-batch2-zh-CN.json"), encoding="utf-8"))[:12]:
    print("   ", json.dumps(key, ensure_ascii=False))
