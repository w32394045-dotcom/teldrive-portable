# -*- coding: utf-8 -*-
"""Final manual wrapping for composite dynamic labels (plural + interpolation).

Every replacement asserts its hit count so a stale pattern cannot silently
no-op or hit the wrong place.
"""
import io
import os
import re
import sys

ROOT = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace\src\teldrive-2\ui\src"

EDITS = [
    # (path, old, new, expected, needs_tPlural)
    ("api/errors.ts",
     'status ? `Request failed with status ${status}` : "Network request failed"',
     'status ? t("Request failed with status {{status}}", { status }) : t("Network request failed")',
     1, False),

    ("features/files/file-manager.tsx",
     '`${ids.length} item${ids.length === 1 ? "" : "s"} moved to trash`',
     'tPlural("{{count}} items moved to trash", ids.length)',
     1, True),
    ("features/files/file-manager.tsx",
     '"Items could not be copied" : "Selected items could not be moved"',
     't("Items could not be copied") : t("Selected items could not be moved")',
     1, False),
    ("features/files/file-manager.tsx",
     '`${selectedFiles.length} download link${selectedFiles.length === 1 ? "" : "s"} copied`',
     'tPlural("{{count}} download links copied", selectedFiles.length)',
     1, True),
    ("features/files/file-manager.tsx",
     '`Paste ${clipboardItems.length} clipboard item${clipboardItems.length === 1 ? "" : "s"}`',
     'tPlural("Paste {{count}} clipboard items", clipboardItems.length)',
     1, True),

    ("components/upload-shelf.tsx",
     '`${summary.completed} files completed`',
     'tPlural("{{count}} files completed", summary.completed)',
     1, True),

    ("components/viewers/pdf-reader.tsx",
     '`Loading ${loadingProgress}%`',
     't("Loading {{progress}}%", { progress: loadingProgress })',
     1, False),

    ("features/files/file-action-dialogs.tsx",
     '`${destinationAction.mode === "move" ? "Move" : "Copy"} ${destinationAction.count} item${destinationAction.count === 1 ? "" : "s"}`',
     'tPlural(destinationAction.mode === "move" ? "Move {{count}} items" : "Copy {{count}} items", destinationAction.count)',
     1, True),

    ("features/files/share-dialog.tsx",
     '`Share ${file.name}`',
     't("Share {{name}}", { name: file.name })',
     1, False),
    ("features/files/share-dialog.tsx",
     '`User ${user.userId}`',
     't("User {{id}}", { id: user.userId })',
     1, False),
    ("features/files/share-dialog.tsx",
     '`User ${grant.granteeUserId}`',
     't("User {{id}}", { id: grant.granteeUserId })',
     1, False),
    ("features/files/share-dialog.tsx",
     '`Expires ${new Date(grant.expiresAt).toLocaleString()}`',
     't("Expires {{date}}", { date: new Date(grant.expiresAt).toLocaleString() })',
     1, False),

    ("routes/_settings.settings.periodic-jobs.tsx",
     '`Resume ${job.id}` : `Pause ${job.id}`',
     't("Resume {{id}}", { id: job.id }) : t("Pause {{id}}", { id: job.id })',
     1, False),
]

failures = []
touched = set()
for rel, old, new, expected, needs_plural in EDITS:
    path = os.path.join(ROOT, rel.replace("/", os.sep))
    text = io.open(path, encoding="utf-8").read()
    if new in text and old not in text:
        print(f"SKIP {rel}: already applied")
        touched.add((path, needs_plural))
        continue
    count = text.count(old)
    if count != expected:
        failures.append(f"{rel}: expected {expected} hit(s) for {old[:70]!r}, found {count}")
        continue
    text = text.replace(old, new, expected)
    io.open(path, "w", encoding="utf-8", newline="").write(text)
    touched.add((path, needs_plural))
    print(f"OK   {rel}: {old[:64]!r}")

# make sure tPlural is imported where it is now used
for path, needs_plural in touched:
    if not needs_plural:
        continue
    text = io.open(path, encoding="utf-8").read()
    if "tPlural" not in text:
        continue
    if re.search(r'import \{ t, tPlural \} from "@/i18n"', text):
        continue
    patched = re.sub(r'import \{ t \} from "@/i18n";', 'import { t, tPlural } from "@/i18n";', text, count=1)
    if patched == text:
        failures.append(f"{os.path.relpath(path, ROOT)}: uses tPlural but no t import found")
    else:
        io.open(path, "w", encoding="utf-8", newline="").write(patched)
        print(f"OK   import tPlural -> {os.path.relpath(path, ROOT)}")

if failures:
    print("\nFAILURES:")
    for row in failures:
        print("  ", row)
    sys.exit(1)
print("\nall composite labels wrapped")
