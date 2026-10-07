# -*- coding: utf-8 -*-
"""Add the keys for the automation section (start at login + mount as a drive)."""
import json
import os
import re
import sys

WS = r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace"
MSG = os.path.join(WS, "src", "teldrive-2", "ui", "src", "i18n", "messages")

TRANSLATIONS = {
    "Keep it running": {
        "zh-CN": "保持常驻", "zh-TW": "保持常駐", "ja": "常駐させる", "ko": "항상 실행",
    },
    "Start Teldrive at login and mount the drive automatically, so your files are available every time you sign in.": {
        "zh-CN": "登录时自动启动 Teldrive 并挂载网络盘，这样每次登录后文件都直接可用。",
        "zh-TW": "登入時自動啟動 Teldrive 並掛載網路磁碟，這樣每次登入後檔案都直接可用。",
        "ja": "ログイン時に Teldrive を自動起動してドライブを割り当てると、サインインするたびにすぐ使えます。",
        "ko": "로그인할 때 Teldrive를 자동으로 시작하고 드라이브를 연결하면, 로그인할 때마다 바로 사용할 수 있습니다.",
    },
    "WebDAV is off": {
        "zh-CN": "WebDAV 未开启", "zh-TW": "WebDAV 未開啟",
        "ja": "WebDAV は無効です", "ko": "WebDAV가 꺼져 있습니다",
    },
    "Turn WebDAV on above before starting it at login or mounting a drive.": {
        "zh-CN": "请先在上方开启 WebDAV，再设置开机自启或挂载。",
        "zh-TW": "請先在上方開啟 WebDAV，再設定開機自啟或掛載。",
        "ja": "先に上の WebDAV を有効にしてから、自動起動やドライブ割り当てを設定してください。",
        "ko": "위에서 WebDAV를 먼저 켠 다음 자동 시작이나 드라이브 연결을 설정하세요.",
    },
    "Start at login": {
        "zh-CN": "开机自启动", "zh-TW": "開機自動啟動",
        "ja": "ログイン時に起動", "ko": "로그인 시 자동 시작",
    },
    "Launch Teldrive quietly when you sign in to Windows, so the mapped drive is always available.": {
        "zh-CN": "登录 Windows 时静默启动 Teldrive，保证映射盘一直可用。",
        "zh-TW": "登入 Windows 時靜默啟動 Teldrive，確保掛載的磁碟隨時可用。",
        "ja": "Windows へのサインイン時に Teldrive を静かに起動し、割り当てたドライブを常に使えるようにします。",
        "ko": "Windows 로그인 시 Teldrive를 조용히 시작해 연결된 드라이브를 항상 사용할 수 있게 합니다.",
    },
    "Mount as a drive": {
        "zh-CN": "挂载为网络盘", "zh-TW": "掛載為網路磁碟",
        "ja": "ドライブとして割り当て", "ko": "드라이브로 연결",
    },
    "Map the WebDAV address above to a drive letter and keep it after a reboot.": {
        "zh-CN": "把上面的 WebDAV 地址映射成一个盘符，并在重启后保留。",
        "zh-TW": "將上方的 WebDAV 位址對應成磁碟機代號，並在重新啟動後保留。",
        "ja": "上の WebDAV アドレスをドライブ文字に割り当て、再起動後も保持します。",
        "ko": "위의 WebDAV 주소를 드라이브 문자로 연결하고 재부팅 후에도 유지합니다.",
    },
    "Mounted at {{drive}}": {
        "zh-CN": "已挂载到 {{drive}}", "zh-TW": "已掛載到 {{drive}}",
        "ja": "{{drive}} に割り当て済み", "ko": "{{drive}}에 연결됨",
    },
    "Not mounted": {
        "zh-CN": "未挂载", "zh-TW": "未掛載", "ja": "未割り当て", "ko": "연결되지 않음",
    },
    "Unmount": {
        "zh-CN": "卸载", "zh-TW": "卸載", "ja": "割り当て解除", "ko": "연결 해제",
    },
    "Mount": {
        "zh-CN": "挂载", "zh-TW": "掛載", "ja": "割り当て", "ko": "연결",
    },
    "Windows needs a small configuration change before a drive can be mapped": {
        "zh-CN": "映射网络盘前，Windows 需要改一处配置",
        "zh-TW": "對應網路磁碟前，Windows 需要改一處設定",
        "ja": "ドライブを割り当てる前に Windows の設定変更が必要です",
        "ko": "드라이브를 연결하려면 Windows 설정을 조금 바꿔야 합니다",
    },
    "Needed": {
        "zh-CN": "需要", "zh-TW": "需要", "ja": "必要", "ko": "필요",
    },
    "Fix with administrator rights": {
        "zh-CN": "以管理员身份修复", "zh-TW": "以管理員身分修復",
        "ja": "管理者権限で修復", "ko": "관리자 권한으로 수정",
    },
    "Copy the command": {
        "zh-CN": "复制命令", "zh-TW": "複製命令",
        "ja": "コマンドをコピー", "ko": "명령 복사",
    },
    "Keeping the drive mounted": {
        "zh-CN": "让映射盘保持可用", "zh-TW": "讓掛載的磁碟保持可用",
        "ja": "割り当てを維持する", "ko": "연결 유지",
    },
    "The drive is mounted, but Teldrive does not start at login — after a reboot you would have to start it yourself, and the drive would disappear.": {
        "zh-CN": "盘已挂载，但 Teldrive 没有设置开机自启——重启后需要你自己启动，映射盘会消失。",
        "zh-TW": "磁碟已掛載，但 Teldrive 沒有設定開機自啟——重新啟動後需要你自己啟動，掛載的磁碟會消失。",
        "ja": "ドライブは割り当て済みですが、Teldrive はログイン時に起動しません。再起動後は手動で起動が必要で、割り当ては消えます。",
        "ko": "드라이브는 연결되어 있지만 Teldrive가 로그인 시 시작하지 않습니다. 재부팅 후에는 직접 시작해야 하며 연결이 사라집니다.",
    },
    "Teldrive starts at login and the drive is mounted automatically.": {
        "zh-CN": "Teldrive 会在登录时自启，映射盘也会自动挂载。",
        "zh-TW": "Teldrive 會在登入時自啟，掛載的磁碟也會自動掛載。",
        "ja": "Teldrive はログイン時に起動し、ドライブも自動で割り当てられます。",
        "ko": "Teldrive가 로그인 시 시작되고 드라이브도 자동으로 연결됩니다.",
    },
    "Teldrive starts at login; mount the drive to make it available straight away.": {
        "zh-CN": "Teldrive 会开机自启；再挂载一次即可立即使用。",
        "zh-TW": "Teldrive 會開機自啟；再掛載一次即可立即使用。",
        "ja": "Teldrive はログイン時に起動します。ドライブを割り当てればすぐ使えます。",
        "ko": "Teldrive는 로그인 시 시작됩니다. 드라이브를 연결하면 바로 사용할 수 있습니다.",
    },
    "Enable both to have the drive ready after every sign-in.": {
        "zh-CN": "两项都开启，登录后就能直接用盘。",
        "zh-TW": "兩項都開啟，登入後就能直接用磁碟。",
        "ja": "両方有効にすると、サインイン後すぐにドライブが使えます。",
        "ko": "둘 다 켜면 로그인 후 바로 드라이브를 쓸 수 있습니다.",
    },
    "Enable start at login": {
        "zh-CN": "开启开机自启", "zh-TW": "開啟開機自啟",
        "ja": "ログイン時の起動を有効にする", "ko": "로그인 시 자동 시작 켜기",
    },
    "Command copied": {
        "zh-CN": "命令已复制", "zh-TW": "命令已複製",
        "ja": "コマンドをコピーしました", "ko": "명령을 복사했습니다",
    },
    "The command could not be copied": {
        "zh-CN": "命令无法复制", "zh-TW": "命令無法複製",
        "ja": "コマンドをコピーできませんでした", "ko": "명령을 복사할 수 없습니다",
    },
    "Start at login updated": {
        "zh-CN": "开机自启设置已更新", "zh-TW": "開機自啟設定已更新",
        "ja": "ログイン時の起動設定を更新しました", "ko": "로그인 시 자동 시작 설정을 업데이트했습니다",
    },
    "The start-at-login setting could not be saved": {
        "zh-CN": "开机自启设置无法保存", "zh-TW": "開機自啟設定無法儲存",
        "ja": "ログイン時の起動設定を保存できませんでした", "ko": "로그인 시 자동 시작 설정을 저장할 수 없습니다",
    },
    "Drive mounted": {
        "zh-CN": "已挂载网络盘", "zh-TW": "已掛載網路磁碟",
        "ja": "ドライブを割り当てました", "ko": "드라이브를 연결했습니다",
    },
    "The drive could not be mounted": {
        "zh-CN": "无法挂载网络盘", "zh-TW": "無法掛載網路磁碟",
        "ja": "ドライブを割り当てできませんでした", "ko": "드라이브를 연결할 수 없습니다",
    },
    "Drive unmounted": {
        "zh-CN": "已卸载网络盘", "zh-TW": "已卸載網路磁碟",
        "ja": "割り当てを解除しました", "ko": "드라이브 연결을 해제했습니다",
    },
    "The drive could not be unmounted": {
        "zh-CN": "无法卸载网络盘", "zh-TW": "無法卸載網路磁碟",
        "ja": "割り当てを解除できませんでした", "ko": "드라이브 연결을 해제할 수 없습니다",
    },
    "The system integration response is malformed.": {
        "zh-CN": "系统集成接口返回的数据格式不正确。",
        "zh-TW": "系統整合介面回應的資料格式不正確。",
        "ja": "システム連携の応答が不正です。",
        "ko": "시스템 통합 응답 형식이 올바르지 않습니다.",
    },
    "The WebClient (WebDAV redirector) service must start automatically.": {
        "zh-CN": "WebClient（WebDAV 重定向器）服务需要设为自动启动。",
        "zh-TW": "WebClient（WebDAV 重新導向器）服務需要設為自動啟動。",
        "ja": "WebClient（WebDAV リダイレクター）サービスを自動起動に設定する必要があります。",
        "ko": "WebClient(WebDAV 리디렉터) 서비스를 자동 시작으로 설정해야 합니다.",
    },
    "Windows only allows Basic authentication over HTTPS by default; this server is http, so BasicAuthLevel must be 2.": {
        "zh-CN": "Windows 默认只允许 HTTPS 使用 Basic 认证，而本机服务是 http，需要把 BasicAuthLevel 设为 2。",
        "zh-TW": "Windows 預設只允許 HTTPS 使用 Basic 認證，而本機服務是 http，需要把 BasicAuthLevel 設為 2。",
        "ja": "Windows は既定で HTTPS 以外の Basic 認証を許可しません。本サーバーは http なので BasicAuthLevel を 2 にする必要があります。",
        "ko": "Windows는 기본적으로 HTTPS에서만 Basic 인증을 허용합니다. 이 서버는 http이므로 BasicAuthLevel을 2로 설정해야 합니다.",
    },
    "Windows caps WebDAV downloads at 50 MB by default; raise FileSizeLimitInBytes for larger files.": {
        "zh-CN": "Windows 默认把 WebDAV 下载限制在 50 MB，需要调高 FileSizeLimitInBytes 才能传大文件。",
        "zh-TW": "Windows 預設把 WebDAV 下載限制在 50 MB，需要調高 FileSizeLimitInBytes 才能傳大檔案。",
        "ja": "Windows は WebDAV のダウンロードを既定で 50 MB に制限します。大きなファイルには FileSizeLimitInBytes の引き上げが必要です。",
        "ko": "Windows는 기본적으로 WebDAV 다운로드를 50MB로 제한합니다. 큰 파일은 FileSizeLimitInBytes를 높여야 합니다.",
    },
    "This feature is only implemented for Windows.": {
        "zh-CN": "该功能目前只支持 Windows。",
        "zh-TW": "該功能目前只支援 Windows。",
        "ja": "この機能は Windows のみに対応しています。",
        "ko": "이 기능은 Windows에서만 지원됩니다.",
    },
}

PLACEHOLDER = re.compile(r"\{\{\s*([\w.$-]+)\s*\}\}")
problems = []

for locale in ["zh-CN", "zh-TW", "ja", "ko"]:
    path = os.path.join(MSG, f"{locale}.json")
    catalog = json.load(open(path, encoding="utf-8"))
    added = 0
    for key, values in TRANSLATIONS.items():
        value = values[locale]
        for name in set(PLACEHOLDER.findall(key)):
            if f"{{{{{name}}}}}" not in value:
                problems.append(f"{locale}: placeholder {{{{{name}}}}} lost in {key!r} -> {value!r}")
        if not value.strip():
            problems.append(f"{locale}: empty translation for {key!r}")
        if key not in catalog:
            added += 1
        catalog[key] = value
    ordered = {key: catalog[key] for key in sorted(catalog)}
    json.dump(ordered, open(path, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
    print(f"{locale}: {len(ordered)} entries ({added} new)")

if problems:
    print("\nPROBLEMS:")
    for row in problems:
        print("  ", row)
    sys.exit(1)
print("\nautomation keys added")
