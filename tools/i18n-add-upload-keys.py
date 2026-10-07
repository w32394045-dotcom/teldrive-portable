# -*- coding: utf-8 -*-
"""Add the upload-failure messages surfaced by the stability pass.

These are the strings a user actually reads when an upload part fails, so they
must not stay English-only, and the interpolated session state is translated
too instead of leaking a raw API enum.
"""
import json
import os
import re
import sys

WS = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace"
MSG = os.path.join(WS, "src", "teldrive-2", "ui", "src", "i18n", "messages")

TRANSLATIONS = {
    "The original file is no longer available. Start the upload again.": {
        "zh-CN": "原文件已不可用，请重新开始上传。",
        "zh-TW": "原始檔案已無法使用，請重新開始上傳。",
        "ja": "元のファイルは利用できません。アップロードをやり直してください。",
        "ko": "원본 파일을 사용할 수 없습니다. 업로드를 다시 시작하세요.",
    },
    "The upload part could not be transferred.": {
        "zh-CN": "上传分片无法传输。",
        "zh-TW": "上傳分片無法傳輸。",
        "ja": "アップロードパートを転送できませんでした。",
        "ko": "업로드 파트를 전송할 수 없습니다.",
    },
    "The upload part timed out.": {
        "zh-CN": "上传分片超时。",
        "zh-TW": "上傳分片逾時。",
        "ja": "アップロードパートがタイムアウトしました。",
        "ko": "업로드 파트 시간이 초과되었습니다.",
    },
    "The upload part stopped transferring data.": {
        "zh-CN": "上传分片已停止传输数据。",
        "zh-TW": "上傳分片已停止傳輸資料。",
        "ja": "アップロードパートのデータ転送が停止しました。",
        "ko": "업로드 파트 데이터 전송이 중단되었습니다.",
    },
    "The upload part was interrupted.": {
        "zh-CN": "上传分片被中断。",
        "zh-TW": "上傳分片被中斷。",
        "ja": "アップロードパートが中断されました。",
        "ko": "업로드 파트가 중단되었습니다.",
    },
    "Upload session is {{state}}.": {
        "zh-CN": "上传会话状态为 {{state}}。",
        "zh-TW": "上傳工作階段狀態為 {{state}}。",
        "ja": "アップロードセッションは {{state}} です。",
        "ko": "업로드 세션이 {{state}} 상태입니다.",
    },
    '"{{name}}" already exists and is not a folder.': {
        "zh-CN": "“{{name}}”已存在，且不是文件夹。",
        "zh-TW": "「{{name}}」已存在，且不是資料夾。",
        "ja": "「{{name}}」は既に存在し、フォルダーではありません。",
        "ko": "{{name}} 이름이 이미 존재하며 폴더가 아닙니다.",
    },
    "The upload session response is malformed.": {
        "zh-CN": "上传会话响应格式不正确。",
        "zh-TW": "上傳工作階段回應格式不正確。",
        "ja": "アップロードセッションの応答が不正です。",
        "ko": "업로드 세션 응답 형식이 올바르지 않습니다.",
    },
    # Upload-session states, shown inside the message above.
    "Open": {"zh-CN": "进行中", "zh-TW": "進行中", "ja": "進行中", "ko": "진행 중"},
    "Completing": {"zh-CN": "正在完成", "zh-TW": "正在完成", "ja": "完了処理中", "ko": "완료 처리 중"},
    "Aborted": {"zh-CN": "已中止", "zh-TW": "已中止", "ja": "中止済み", "ko": "중단됨"},
    "Expired": {"zh-CN": "已过期", "zh-TW": "已過期", "ja": "期限切れ", "ko": "만료됨"},
}

PLACEHOLDER = re.compile(r"\{\{\s*([\w.$-]+)\s*\}\}")
problems = []

for locale in ["zh-CN", "zh-TW", "ja", "ko"]:
    path = os.path.join(MSG, f"{locale}.json")
    catalog = json.load(open(path, encoding="utf-8"))
    changed = 0
    for key, values in TRANSLATIONS.items():
        value = values[locale]
        for name in set(PLACEHOLDER.findall(key)):
            if f"{{{{{name}}}}}" not in value:
                problems.append(f"{locale}: placeholder {{{{{name}}}}} lost in {key!r} -> {value!r}")
        if not value.strip():
            problems.append(f"{locale}: empty translation for {key!r}")
        if catalog.get(key) != value:
            catalog[key] = value
            changed += 1
    ordered = {key: catalog[key] for key in sorted(catalog)}
    json.dump(ordered, open(path, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
    print(f"{locale}: {len(ordered)} entries ({changed} added/updated)")

if problems:
    print("\nPROBLEMS:")
    for row in problems:
        print("  ", row)
    sys.exit(1)
print("\nupload messages added")
