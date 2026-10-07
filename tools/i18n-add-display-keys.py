# -*- coding: utf-8 -*-
"""Add the display keys introduced by the UI-display pass.

Three groups:
  1. plural keys — `tPlural()` looks up "<key>" and "<key>.one"; the CJK
     catalogs had no "<key>" entry at all, so every pluralised message was
     rendering in English. CJK does not inflect for number, so one form serves
     both counts and only en.json needs the ".one" override.
  2. enum/identifier labels — file kind, job kind, task type, activity type,
     storage category, user role.
  3. small copy fixes (a toast, pluralised stat details).

Validates placeholders and empty values, then rewrites each catalog sorted,
matching the existing json.dump(ensure_ascii=False, indent=1) convention.
"""
import json
import os
import re
import sys

WS = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace"
MSG = os.path.join(WS, "src", "teldrive-2", "ui", "src", "i18n", "messages")

TRANSLATIONS = {
    # --- plural keys (count + measure word; invariant in CJK) ---
    "{{count}} files": {
        "zh-CN": "{{count}} 个文件", "zh-TW": "{{count}} 個檔案",
        "ja": "{{count}} 件のファイル", "ko": "파일 {{count}}개",
    },
    "{{count}} folders": {
        "zh-CN": "{{count}} 个文件夹", "zh-TW": "{{count}} 個資料夾",
        "ja": "{{count}} 件のフォルダー", "ko": "폴더 {{count}}개",
    },
    "{{count}} sessions": {
        "zh-CN": "{{count}} 个会话", "zh-TW": "{{count}} 個工作階段",
        "ja": "{{count}} 件のセッション", "ko": "세션 {{count}}개",
    },
    "{{count}} selected": {
        "zh-CN": "已选择 {{count}} 项", "zh-TW": "已選取 {{count}} 項",
        "ja": "{{count}} 件を選択中", "ko": "{{count}}개 선택됨",
    },
    "{{count}} files completed": {
        "zh-CN": "{{count}} 个文件已完成", "zh-TW": "{{count}} 個檔案已完成",
        "ja": "{{count}} 件のファイルが完了", "ko": "파일 {{count}}개 완료",
    },
    "{{count}} items moved to trash": {
        "zh-CN": "已将 {{count}} 个项目移入回收站", "zh-TW": "已將 {{count}} 個項目移至垃圾桶",
        "ja": "{{count}} 件の項目をごみ箱に移動しました", "ko": "항목 {{count}}개를 휴지통으로 이동했습니다",
    },
    "{{count}} download links copied": {
        "zh-CN": "已复制 {{count}} 个下载链接", "zh-TW": "已複製 {{count}} 個下載連結",
        "ja": "{{count}} 件のダウンロードリンクをコピーしました", "ko": "다운로드 링크 {{count}}개를 복사했습니다",
    },
    "Paste {{count}} clipboard items": {
        "zh-CN": "粘贴 {{count}} 个剪贴板项目", "zh-TW": "貼上 {{count}} 個剪貼簿項目",
        "ja": "{{count}} 件のクリップボード項目を貼り付け", "ko": "클립보드 항목 {{count}}개 붙여넣기",
    },
    "Move {{count}} items": {
        "zh-CN": "移动 {{count}} 个项目", "zh-TW": "移動 {{count}} 個項目",
        "ja": "{{count}} 件の項目を移動", "ko": "항목 {{count}}개 이동",
    },
    "Copy {{count}} items": {
        "zh-CN": "复制 {{count}} 个项目", "zh-TW": "複製 {{count}} 個項目",
        "ja": "{{count}} 件の項目をコピー", "ko": "항목 {{count}}개 복사",
    },
    # --- file kind ---
    "File": {"zh-CN": "文件", "zh-TW": "檔案", "ja": "ファイル", "ko": "파일"},
    # --- storage categories ---
    "Archives": {"zh-CN": "归档", "zh-TW": "封存", "ja": "アーカイブ", "ko": "압축 파일"},
    "Audio": {"zh-CN": "音频", "zh-TW": "音訊", "ja": "オーディオ", "ko": "오디오"},
    "Documents": {"zh-CN": "文档", "zh-TW": "文件", "ja": "ドキュメント", "ko": "문서"},
    "Images": {"zh-CN": "图片", "zh-TW": "圖片", "ja": "画像", "ko": "이미지"},
    "Video": {"zh-CN": "视频", "zh-TW": "影片", "ja": "動画", "ko": "동영상"},
    "Other": {"zh-CN": "其他", "zh-TW": "其他", "ja": "その他", "ko": "기타"},
    # --- storage activity feed ---
    "File added": {"zh-CN": "已添加文件", "zh-TW": "已新增檔案", "ja": "ファイルを追加しました", "ko": "파일 추가됨"},
    "File moved to Trash": {"zh-CN": "文件已移入回收站", "zh-TW": "檔案已移至垃圾桶", "ja": "ファイルをごみ箱に移動しました", "ko": "파일이 휴지통으로 이동됨"},
    "File restored": {"zh-CN": "文件已恢复", "zh-TW": "檔案已還原", "ja": "ファイルを復元しました", "ko": "파일 복원됨"},
    "File permanently deleted": {"zh-CN": "文件已永久删除", "zh-TW": "檔案已永久刪除", "ja": "ファイルを完全に削除しました", "ko": "파일 영구 삭제됨"},
    "Upload completed": {"zh-CN": "上传已完成", "zh-TW": "上傳已完成", "ja": "アップロード完了", "ko": "업로드 완료"},
    "Upload aborted": {"zh-CN": "上传已中止", "zh-TW": "上傳已中止", "ja": "アップロード中止", "ko": "업로드 중단됨"},
    "Upload expired": {"zh-CN": "上传已过期", "zh-TW": "上傳已過期", "ja": "アップロード期限切れ", "ko": "업로드 만료됨"},
    "Share created": {"zh-CN": "已创建分享", "zh-TW": "已建立分享", "ja": "共有を作成しました", "ko": "공유 생성됨"},
    "Share removed": {"zh-CN": "已移除分享", "zh-TW": "已移除分享", "ja": "共有を削除しました", "ko": "공유 제거됨"},
    "Storage channel added": {"zh-CN": "已添加存储频道", "zh-TW": "已新增儲存頻道", "ja": "ストレージチャンネルを追加しました", "ko": "스토리지 채널 추가됨"},
    "Storage channel updated": {"zh-CN": "已更新存储频道", "zh-TW": "已更新儲存頻道", "ja": "ストレージチャンネルを更新しました", "ko": "스토리지 채널 업데이트됨"},
    "Storage channel removed": {"zh-CN": "已移除存储频道", "zh-TW": "已移除儲存頻道", "ja": "ストレージチャンネルを削除しました", "ko": "스토리지 채널 제거됨"},
    # --- misc copy ---
    "{{name}} restored": {
        "zh-CN": "{{name}} 已恢复", "zh-TW": "{{name}} 已還原",
        "ja": "{{name}} を復元しました", "ko": "{{name}} 복원됨",
    },
    "{{category}} storage": {
        "zh-CN": "{{category}} 存储", "zh-TW": "{{category}} 儲存空間",
        "ja": "{{category}} のストレージ", "ko": "{{category}} 저장소",
    },
    # --- job kinds / task types shown through tIdentifier() ---
    "Provision Bots": {"zh-CN": "配置机器人", "zh-TW": "佈建機器人", "ja": "ボットのプロビジョニング", "ko": "봇 프로비저닝"},
    "Cleanup Uploads": {"zh-CN": "清理上传", "zh-TW": "清理上傳", "ja": "アップロードのクリーンアップ", "ko": "업로드 정리"},
    "Cleanup User Events": {"zh-CN": "清理用户事件", "zh-TW": "清理使用者事件", "ja": "ユーザーイベントのクリーンアップ", "ko": "사용자 이벤트 정리"},
    "Cleanup Orphaned Telegram Parts": {"zh-CN": "清理孤立的 Telegram 分片", "zh-TW": "清理孤立的 Telegram 分片", "ja": "孤立した Telegram パートのクリーンアップ", "ko": "고아 Telegram 파트 정리"},
    "Purge Pending Files": {"zh-CN": "清除待处理文件", "zh-TW": "清除待處理檔案", "ja": "保留中のファイルを削除", "ko": "대기 중 파일 제거"},
    "Cleanup Trash": {"zh-CN": "清理回收站", "zh-TW": "清理垃圾桶", "ja": "ごみ箱のクリーンアップ", "ko": "휴지통 정리"},
    "Upload Batch": {"zh-CN": "批量上传", "zh-TW": "批次上傳", "ja": "バッチアップロード", "ko": "일괄 업로드"},
    "Upload Source": {"zh-CN": "源上传", "zh-TW": "來源上傳", "ja": "ソースアップロード", "ko": "소스 업로드"},
}

# Singular overrides for the English source text so `tPlural` reads naturally.
EN_SINGULARS = {
    "{{count}} folders.one": "{{count}} folder",
    "{{count}} sessions.one": "{{count}} session",
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

en_path = os.path.join(MSG, "en.json")
en = json.load(open(en_path, encoding="utf-8"))
for key, value in EN_SINGULARS.items():
    en.setdefault(key, value)
json.dump({key: en[key] for key in sorted(en)}, open(en_path, "w", encoding="utf-8"),
          ensure_ascii=False, indent=1)
print(f"en: {len(en)} singular overrides")

if problems:
    print("\nPROBLEMS:")
    for row in problems:
        print("  ", row)
    sys.exit(1)
print("\nall catalogs updated")
