# -*- coding: utf-8 -*-
"""Generate the Windows one-click launcher files for the teldrive portable bundle.

.bat files are written as GBK (cp936) because this machine's console code page is 936;
that keeps Chinese text readable in cmd without chcp games.
The readme is written as UTF-8 with BOM so Notepad shows it correctly.
"""
import base64
import os
import secrets
import sys

BUNDLE = sys.argv[1] if len(sys.argv) > 1 else r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace\dist\teldrive-2-win64"

START_BAT = r"""@echo off
setlocal EnableExtensions
cd /d "%~dp0"
title Teldrive 一键启动

set "ROOT=%~dp0"
set "PGBIN=%ROOT%pgsql\bin"
set "PGDATA=%ROOT%data\pgdata"
set "PGPORT=5433"
set "HTTPPORT=8080"
set "LOGDIR=%ROOT%data"
set "STARTED_PG="

echo ============================================================
echo    Teldrive  Telegram 网盘  一键启动
echo ============================================================
echo.

if not exist "%ROOT%teldrive.exe" ( echo [错误] 找不到 teldrive.exe & goto :fail )
if not exist "%PGBIN%\postgres.exe" ( echo [错误] 找不到 pgsql\bin\postgres.exe & goto :fail )
if not exist "%ROOT%config.toml" ( echo [错误] 找不到 config.toml & goto :fail )

rem 公开发布版用占位符密钥，首次启动在这里生成随机密钥
call "%~dp0_init-keys.bat"
if errorlevel 1 goto :fail

tasklist /fi "imagename eq teldrive.exe" 2>nul | find /i "teldrive.exe" >nul
if not errorlevel 1 (
    echo [提示] Teldrive 已经在运行，正在打开浏览器...
    start "" "http://127.0.0.1:%HTTPPORT%"
    exit /b 0
)

if not exist "%LOGDIR%" mkdir "%LOGDIR%"

if exist "%PGDATA%\PG_VERSION" goto :initdone
echo [1/4] 首次运行：初始化内置 PostgreSQL（约需 1 分钟，请勿关闭窗口）...
"%PGBIN%\initdb.exe" -D "%PGDATA%" -U postgres -A trust --encoding=UTF8 --locale=C > "%LOGDIR%\initdb.log" 2>&1
if errorlevel 1 goto :initfail
echo       创建 teldrive 数据库...
echo CREATE DATABASE teldrive;| "%PGBIN%\postgres.exe" --single -D "%PGDATA%" postgres >> "%LOGDIR%\initdb.log" 2>&1
goto :initdone
:initfail
echo [错误] 数据库初始化失败，请查看 data\initdb.log
goto :fail
:initdone
if exist "%PGDATA%\PG_VERSION" echo [1/4] 内置数据库已就绪

echo [2/4] 启动内置 PostgreSQL（127.0.0.1:%PGPORT%）...
"%PGBIN%\pg_ctl.exe" status -D "%PGDATA%" >nul 2>&1
if not errorlevel 1 (
    echo       数据库已在运行
    goto :pgready
)
"%PGBIN%\pg_ctl.exe" -D "%PGDATA%" -l "%LOGDIR%\postgres.log" -o "-p %PGPORT% -c listen_addresses=127.0.0.1" -w start >nul 2>&1
if errorlevel 1 goto :pgfail
set "STARTED_PG=1"
:pgready

echo [3/4] 启动 Teldrive 服务：http://127.0.0.1:%HTTPPORT%
start "teldrive-browser" /min "%ROOT%_open-browser.bat" %HTTPPORT%
echo [4/4] 服务运行中。关闭本窗口或按 Ctrl+C 即可停止服务。
echo.
echo ------------------------------------------------------------
echo  浏览器稍后会自动打开。首次使用请在弹出的页面里登录 Telegram。
echo ------------------------------------------------------------
echo.

"%ROOT%teldrive.exe" run -c "%ROOT%config.toml"
set "CODE=%ERRORLEVEL%"
echo.
echo Teldrive 已退出（代码 %CODE%）。
if defined STARTED_PG (
    echo 正在停止内置 PostgreSQL...
    "%PGBIN%\pg_ctl.exe" -D "%PGDATA%" -m fast -w stop >nul 2>&1
)
echo.
pause
exit /b %CODE%

:pgfail
echo [错误] PostgreSQL 启动失败，请查看 data\postgres.log
goto :fail
:fail
echo.
pause
exit /b 1
"""

STOP_BAT = r"""@echo off
setlocal EnableExtensions
cd /d "%~dp0"
title 停止 Teldrive
echo 正在停止 Teldrive ...
taskkill /im teldrive.exe /f >nul 2>&1
if errorlevel 1 ( echo   Teldrive 未在运行 ) else ( echo   Teldrive 已停止 )

if exist "pgsql\bin\pg_ctl.exe" (
    if exist "data\pgdata\PG_VERSION" (
        "pgsql\bin\pg_ctl.exe" status -D "data\pgdata" >nul 2>&1
        if not errorlevel 1 (
            echo   正在停止内置 PostgreSQL ...
            "pgsql\bin\pg_ctl.exe" -D "data\pgdata" -m fast -w stop >nul 2>&1
            echo   PostgreSQL 已停止
        )
    )
)
echo 完成。
ping -n 4 127.0.0.1 >nul 2>&1
exit /b 0
"""

OPEN_BROWSER_BAT = r"""@echo off
set "PORT=%~1"
if "%PORT%"=="" set "PORT=8080"
rem 先等端口真正就绪（最多 300 秒），避免浏览器打开时服务还没起来
powershell -NoProfile -ExecutionPolicy Bypass -Command "for($i=0;$i -lt 300;$i++){ try{ $c=New-Object Net.Sockets.TcpClient; $c.Connect('127.0.0.1',%PORT%); $c.Close(); exit 0 } catch { Start-Sleep -Milliseconds 1000 } }; exit 1"
if errorlevel 1 (
    echo [提示] 服务启动较慢，请手动打开 http://127.0.0.1:%PORT%
    ping -n 6 127.0.0.1 >nul 2>&1
    exit /b 0
)
start "" "http://127.0.0.1:%PORT%"
exit /b 0
"""

INIT_KEYS_BAT = r"""@echo off
setlocal EnableExtensions
rem 公开发布版：config.toml 里是占位符，首次启动时在这里生成随机密钥
findstr /C:"__SIGNING_KEY__" "%~dp0config.toml" >nul 2>&1
if errorlevel 1 exit /b 0
echo       首次运行：生成随机 signing-key / data-key ...
powershell -NoProfile -ExecutionPolicy Bypass -Command "$ErrorActionPreference='Stop'; $rng=[System.Security.Cryptography.RandomNumberGenerator]::Create(); $b=New-Object byte[] 68; $rng.GetBytes($b); $sign=[Convert]::ToBase64String($b[0..35]); $data=[Convert]::ToBase64String($b[36..67]); $p='%~dp0config.toml'; $c=[System.IO.File]::ReadAllText($p); $c=$c.Replace('__SIGNING_KEY__',$sign).Replace('__DATA_KEY__',$data); [System.IO.File]::WriteAllText($p,$c,(New-Object System.Text.UTF8Encoding $false))"
if errorlevel 1 (
    echo [错误] 生成密钥失败：需要系统自带 PowerShell
    exit /b 1
)
findstr /C:"__SIGNING_KEY__" "%~dp0config.toml" >nul 2>&1
if not errorlevel 1 (
    echo [错误] 生成密钥后占位符仍在，请检查 config.toml
    exit /b 1
)
exit /b 0
"""

CONFIG_TOML = r'''# Teldrive 本地运行配置（一键运行包自动生成）
# 修改后重启 start.bat 生效。所有项也可以用在环境变量里覆盖，例如 TELDRIVE_HTTP_ADDRESS。

[http]
# 仅监听本机，不对局域网/公网开放。如果 8080 被占用，改成其它端口即可。
address = "127.0.0.1:8080"
read-header-timeout = "10s"
read-timeout = "1h"
write-timeout = "1h"
idle-timeout = "2m"
shutdown-timeout = "10s"
trusted-proxies = []

[database]
# 指向随包附带的便携 PostgreSQL（data\pgdata），端口 5433，无密码，仅本机。
schema = "teldrive"
url = "postgres://postgres@127.0.0.1:5433/teldrive?sslmode=disable"
application-name = "teldrive-v2"
max-connections = 25
min-connections = 2
max-connection-idle = "5m"
max-connection-life = "30m"
health-check-interval = "30s"
connect-timeout = "10s"
auto-migrate-legacy = true

[telegram]
# remote = 真正连接 Telegram（需要有效的 api_id / api_hash）。
# 想先离线试跑可改成 "filesystem"，文件会落在 local-root 目录里。
backend = "remote"
local-root = "./data/local-telegram"
app-id = 2496
app-hash = "8da85b0d5bfe62527e5b244c209159c3"
device-model = "Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/116.0"
system-version = "Win32"
app-version = "6.1.4 K"
language-code = "en"
system-language-code = "en-US"
language-pack = "webk"
dial-timeout = "10s"
reconnect-timeout = "5m"
max-retries = 10
rate-limit = true
rate-interval = "50ms"
rate-burst = 10
proxy = ""
upload-threads = 8
download-bots = 0
download-client-pool = false
download-read-buffers = 32
download-read-parallel = 4
randomize-part-names = true
auto-channel-create = true
bot-rotation-backend = "memory"
channel-part-limit = 500000
channel-name-prefix = "teldrive"

[telegram.mtproxy]
address = ""
secret = ""

[encryption]
active-key-version = 0
keys = ""

[security]
# 本机安装时随机生成。更换 data-key 会导致已保存的 Telegram 凭据无法解密，请勿随意修改。
signing-key = "%(signing_key)s"
data-key = "%(data_key)s"
issuer = "teldrive-v2"
# 留空表示允许任何 Telegram 账号登录；填入用户名可限定为白名单，例如 ["yourname"]。
allowed-users = []
access-token-ttl = "15m"
refresh-token-ttl = "720h"
login-flow-ttl = "10m"

[uploads]
session-ttl = "168h"
hashing-enabled = true
local-import-roots = []

[cache.memory]
size = "5MB"

[events]
batch-size = 100
max-connections-per-user = 5
heartbeat = "20s"
write-timeout = "10s"
ticket-ttl = "2m"
retention = "168h"
cleanup-interval = "1h"
connect-timeout = "10s"
ping-interval = "5s"
reconnect-min = "100ms"
reconnect-max = "30s"

[logging]
log-level = "info"
log-format = "text"
'''

README = r"""========================================================================
 Teldrive 2  Windows 一键运行包
========================================================================

【怎么用】
  双击 start.bat  →  等几秒，浏览器自动打开 http://127.0.0.1:8080
  停止：关闭那个黑色窗口（或按 Ctrl+C），也可以双击 stop.bat

  第一次启动会自动初始化内置数据库，大约需要 1 分钟，
  之后每次启动在 5 秒左右。

【里面有什么】
  start.bat       一键启动（启动内置数据库 + 启动 Teldrive + 打开浏览器）
  stop.bat        停止 Teldrive 和内置数据库
  teldrive.exe    服务主程序（网页界面已经打包进这个 exe，无需联网下载）
  config.toml     配置文件（端口、Telegram api_id、登录白名单等）
  pgsql\          便携版 PostgreSQL 17，绿色免安装，只监听 127.0.0.1:5433
  data\           运行数据：pgdata 数据库、postgres.log、initdb.log
  使用说明.txt    本文件

【首次使用要做的事】
  1. 打开网页后，用你的 Telegram 账号登录（需要手机接收验证码）。
     电话号码必须带国家代码、以 + 开头，例如 +8613800138000；
     只填 138... 会被服务器拒绝。空格和横线随便打，程序会自动整理。
  2. 建议先看页面里的“设置”，确认存储频道等配置。
  3. app-id / app-hash 用的是官方示例值。如果登录时报 API 相关错误，
     到 https://my.telegram.org 申请自己的 api_id / api_hash，
     填进 config.toml 的 [telegram] 段后重启。

【界面语言：简繁中日韩随便切】
  顶部右上角（登录页）和「设置 → 外观 → Language」都有语言切换器，
  可选 English / 简体中文 / 繁體中文 / 日本語 / 한국어，选中即时生效并记住。
  首次打开会跟随系统/浏览器语言自动选择。

  注意：请【不要】再用 Chrome 的"翻译此页 / 始终翻译英文"。
  Google 翻译会重写页面 DOM，React 接着更新时就会报
  "Failed to execute 'insertBefore' on 'Node' ..."，表现为按钮点不动。
  现在页面已自带四种语言，不再需要浏览器翻译（页面也声明了 notranslate）。

【重要提醒】
  * 服务只监听 127.0.0.1，同一台电脑才能访问，不对外网开放。
  * 如果 8080 端口被占用：编辑 config.toml 的 http.address（例如改成 127.0.0.1:8090）。
  * 聊天/下载目录（data\）不要删；里面是数据库。换机器时整个文件夹拷走即可。
  * Telegram 对 API 调用频率有严格限制，请勿滥用，否则账号可能被限制。

【常见问题】
  启动窗口一闪而过或报错：
     手动双击 start.bat 看提示；数据库错误看 data\initdb.log 和 data\postgres.log。
  提示 Teldrive 已在运行：
     说明服务已经起来了，直接打开 http://127.0.0.1:8080 即可。
  想彻底重置：
     先运行 stop.bat，再删除 data\ 目录，重新双击 start.bat（数据会全部清空）。

【本包是怎么构建的】
  源码：teldrive-2（github.com/tgdrive/teldrive v2）
  构建：Go 1.27.1 + bun 1.4.2 构建前端 → 前端资源嵌入 teldrive.exe
  数据库：便携 PostgreSQL 17.11（官方 Windows x64 构建，免安装）
  本机补丁（v2.0.0-p1，2026-10-07）：
    1) 页面声明 notranslate，避免浏览器整页翻译把 React 页面弄崩；
    2) 登录页手机号自动规范化（去空格/横线、00 转 +），格式不对时给红色提示；
    3) 服务端 400 由“request could not be decoded”改为指出具体字段与原因。
========================================================================
"""

os.makedirs(BUNDLE, exist_ok=True)

# `--public` produces a distributable bundle: config.toml carries placeholders
# instead of this machine's real keys, and start.bat generates fresh random keys
# on first run. Never ship a bundle that contains someone's live secrets.
PUBLIC = "--public" in sys.argv

cfg_path = os.path.join(BUNDLE, "config.toml")
if PUBLIC:
    signing_key = "__SIGNING_KEY__"
    data_key = "__DATA_KEY__"
    with open(cfg_path, "w", encoding="utf-8", newline="\r\n") as fh:
        fh.write(CONFIG_TOML % {"signing_key": signing_key, "data_key": data_key})
    print("config.toml written with placeholders (public build)")
elif os.path.exists(cfg_path):
    print("config.toml already exists, keeping it")
else:
    signing_key = secrets.token_urlsafe(36)          # >= 32 chars
    data_key = base64.b64encode(secrets.token_bytes(32)).decode()  # 32 bytes base64
    with open(cfg_path, "w", encoding="utf-8", newline="\r\n") as fh:
        fh.write(CONFIG_TOML % {"signing_key": signing_key, "data_key": data_key})
    print("config.toml written (fresh random keys)")


def write_gbk(name, text):
    path = os.path.join(BUNDLE, name)
    with open(path, "w", encoding="gbk", newline="\r\n") as fh:
        fh.write(text)
    print("wrote", name)


def write_utf8_bom(name, text):
    path = os.path.join(BUNDLE, name)
    with open(path, "wb") as fh:
        fh.write(b"\xef\xbb\xbf" + text.replace("\n", "\r\n").encode("utf-8"))
    print("wrote", name)


write_gbk("start.bat", START_BAT)
write_gbk("stop.bat", STOP_BAT)
write_gbk("_open-browser.bat", OPEN_BROWSER_BAT)
write_gbk("_init-keys.bat", INIT_KEYS_BAT)
write_utf8_bom("使用说明.txt", README)
print("bundle files ready:", BUNDLE)
