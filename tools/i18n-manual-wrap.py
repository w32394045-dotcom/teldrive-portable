# -*- coding: utf-8 -*-
"""Render-time t() wraps for module-level label tables.

These strings live in module-scope constants that are evaluated once at import
time, so wrapping the literal would freeze the language. Instead we translate at
the render site. Every replacement asserts its own hit count so a stale pattern
cannot silently no-op or hit the wrong place.
"""
import io
import os
import sys

ROOT = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace\src\teldrive-2\ui\src"

# (relative path, old, new, expected occurrences)
EDITS = [
    ("routes/_settings.tsx", '{activeLabel ?? "Settings"}', '{t(activeLabel ?? "Settings")}', 1),
    ("routes/_settings.tsx", "            {group.label}", "            {t(group.label)}", 1),
    ("routes/_settings.tsx", "<span>{item.label}</span>", "<span>{t(item.label)}</span>", 1),
    ("routes/__root.tsx", "          {item.label}\n        </span>", "          {t(item.label)}\n        </span>", 1),
    ("routes/__root.tsx", "{title}</p>", "{t(title)}</p>", 1),
    ("routes/tasks.tsx", "{selected.label}</span>", "{t(selected.label)}</span>", 1),
    ("routes/tasks.tsx", "textValue={item.label}", "textValue={t(item.label)}", 1),
    ("routes/_settings.settings.periodic-jobs.tsx", "\n                  {preset.label}\n",
     "\n                  {t(preset.label)}\n", 1),
    ("components/task-launcher.tsx", "textValue={item.label}", "textValue={t(item.label)}", 1),
    ("components/task-launcher.tsx", '<div className="text-sm font-medium">{item.label}</div>',
     '<div className="text-sm font-medium">{t(item.label)}</div>', 1),
    ("components/task-launcher.tsx", '<div className="truncate text-xs text-muted">{item.description}</div>',
     '<div className="truncate text-xs text-muted">{t(item.description)}</div>', 1),
    ("components/task-launcher.tsx", "                      {group.label}", "                      {t(group.label)}", 1),
    ("components/task-launcher.tsx", "                            {item.label}", "                            {t(item.label)}", 1),
    ("components/task-launcher.tsx", "{selected.label}", "{t(selected.label)}", 1),
    ("components/task-launcher.tsx", "{selected.description}", "{t(selected.description)}", 1),
]

failures = []
for rel, old, new, expected in EDITS:
    path = os.path.join(ROOT, rel.replace("/", os.sep))
    with io.open(path, encoding="utf-8") as fh:
        text = fh.read()
    if new in text and old not in text:
        print(f"SKIP {rel}: already applied")
        continue
    count = text.count(old)
    if count != expected:
        failures.append(f"{rel}: expected {expected} hit(s) for {old!r}, found {count}")
        continue
    text = text.replace(old, new, expected)
    with io.open(path, "w", encoding="utf-8", newline="") as fh:
        fh.write(text)
    print(f"OK  {rel}: {old[:60]!r} -> t(...)")

if failures:
    print("\nFAILURES:")
    for row in failures:
        print("  ", row)
    sys.exit(1)
print("\nall render-time wraps applied")
