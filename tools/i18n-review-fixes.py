# -*- coding: utf-8 -*-
"""Apply reviewed translation fixes across all catalogs.

Sources of truth for these fixes:
  * a code-level review of the real call sites (share-dialog, tasks, search-controls,
    bots settings) that showed several batch-3 strings were context-free guesses
  * the existing sibling keys ("Make admin" / "Make user") for verb forms
"""
import json
import os

WS = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace"
MSG = os.path.join(WS, "src", "teldrive-2", "ui", "src", "i18n", "messages")

FIXES = {
    "zh-CN": {
        # "Make" was the verb "make editor/viewer", not a brand; the source now
        # uses two explicit keys so the phrase reads naturally.
        "Make editor": "设为编辑者",
        "Make viewer": "设为查看者",
        "Clean {{value0}} tasks?": "清理 {{value0}} 任务？",
        "Check the modified-date range": "检查修改日期范围",
    },
    "zh-TW": {
        "Make editor": "設為編輯者",
        "Make viewer": "設為查看者",
        "Added {{value0}}": "新增於 {{value0}}",
        "After {{value0}}": "晚於 {{value0}}",
        "Before {{value0}}": "早於 {{value0}}",
        "Check the modified-date range": "請檢查修改日期範圍",
        "Clean {{value0}} tasks?": "要清理 {{value0}} 工作嗎？",
        "Failed to clean {{cleanupStatus}} tasks": "無法清理 {{cleanupStatus}} 工作",
        "missing part": "個分片遺失",
        "parts ·": "個分片 ·",
        "{{value0}} added today": "今日新增 {{value0}}",
    },
    "ja": {
        "Make editor": "編集者にする",
        "Make viewer": "閲覧者にする",
        "Added {{value0}}": "{{value0}} に追加",
        "{{value0}} added today": "本日 {{value0}} 追加",
        "Clean {{value0}} tasks?": "{{value0}} タスクをクリーンアップしますか？",
    },
    "ko": {
        "Make editor": "편집자로 지정",
        "Make viewer": "뷰어로 지정",
        "Clean {{value0}} tasks?": "{{value0}} 작업을 정리할까요?",
    },
}

for locale, fixes in FIXES.items():
    path = os.path.join(MSG, f"{locale}.json")
    catalog = json.load(open(path, encoding="utf-8"))
    changed = 0
    for key, value in fixes.items():
        if catalog.get(key) != value:
            catalog[key] = value
            changed += 1
    ordered = {key: catalog[key] for key in sorted(catalog)}
    json.dump(ordered, open(path, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
    print(f"{locale}: applied {changed} fix(es), {len(ordered)} entries")
