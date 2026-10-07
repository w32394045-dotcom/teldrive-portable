# -*- coding: utf-8 -*-
"""Un-wrap technical strings that pass 3 wrongly wrapped in t().

CSS class strings, font stacks and request URLs must never go through the
translation layer: a translator could legitimately "translate" them and break
styling or network calls.
"""
import io
import os
import sys

ROOT = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace\src\teldrive-2\ui\src"

EDITS = [
    ("routes/__root.tsx",
     'mobile ? t("w-[min(19rem,86vw)] shadow-2xl")',
     'mobile ? "w-[min(19rem,86vw)] shadow-2xl"'),

    ("components/file-preview-dialog.tsx",
     't("bg-[radial-gradient(circle_at_50%_20%,color-mix(in_oklch,var(--muted-background)_70%,transparent),var(--background)_65%)]")',
     '"bg-[radial-gradient(circle_at_50%_20%,color-mix(in_oklch,var(--muted-background)_70%,transparent),var(--background)_65%)]"'),

    ("features/files/foliate-reader.ts",
     'return t("Iowan Old Style, Charter, \\"Bitstream Charter\\", Georgia, serif");',
     "return 'Iowan Old Style, Charter, \"Bitstream Charter\", Georgia, serif';"),
    ("features/files/foliate-reader.ts",
     'return t("Avenir Next, Avenir, \\"Segoe UI\\", sans-serif");',
     "return 'Avenir Next, Avenir, \"Segoe UI\", sans-serif';"),

    ("features/uploads/store.ts",
     't("/v1/uploads/{{value0}}/parts?{{query}}", { value0: encodeURIComponent(uploadId), query })',
     '`/v1/uploads/${encodeURIComponent(uploadId)}/parts?${query}`'),
    ("features/uploads/store.ts",
     't("/v1/files?{{query}}", { query })',
     '`/v1/files?${query}`'),
    ("routes/share.$token.tsx",
     't("/v1/public/shares/{{value0}}/files?{{value1}}", { value0: encodeURIComponent(token), value1: params.toString() })',
     '`/v1/public/shares/${encodeURIComponent(token)}/files?${params.toString()}`'),
]

failures = []
for rel, old, new in EDITS:
    path = os.path.join(ROOT, rel.replace("/", os.sep))
    text = io.open(path, encoding="utf-8").read()
    if old not in text:
        if new in text:
            print(f"SKIP {rel}: already unwrapped")
            continue
        failures.append(f"{rel}: pattern not found: {old[:70]!r}")
        continue
    if text.count(old) != 1:
        failures.append(f"{rel}: pattern is not unique ({text.count(old)}x): {old[:60]!r}")
        continue
    io.open(path, "w", encoding="utf-8", newline="").write(text.replace(old, new, 1))
    print(f"OK   {rel}: unwrapped {old[:56]!r}")

if failures:
    print("\nFAILURES:")
    for row in failures:
        print("  ", row)
    sys.exit(1)
print("\ntechnical strings are no longer translated")
