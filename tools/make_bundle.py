# -*- coding: utf-8 -*-
"""Generate the launcher files for the teldrive portable bundle.

The released folder deliberately has a single entry point: `teldrive.exe`. The
executable itself generates first-run keys, initialises and starts the bundled
PostgreSQL, opens the browser, and stops the database again on exit, so there are
no .bat files for the user to choose between.

This script therefore only writes `config.toml` (with key placeholders for
public builds) and the UTF-8-BOM instruction file.
"""
import base64
import os
import secrets
import sys

BUNDLE = sys.argv[1] if len(sys.argv) > 1 else r"C:\Users\ptfm\Documents\deepseek-harness\default-workspace\dist\teldrive-2-win64"

CONFIG_TOML = r'''# Teldrive 本地运行配置（一键运行包自动生成）
# 修改后重启 teldrive.exe 生效。所有项也可以用在环境变量里覆盖，例如 TELDRIVE_HTTP_ADDRESS。

[http]
# 仅监听本机，不对局域网/公网开放。
# 这里的端口只是「首选」：被占用时 teldrive.exe 会自动改用后面第一个空闲端口，
# 并把实际端口记在 data\port 里。之所以不用 8080，是因为那个端口几乎所有开发
# 工具都会抢，撞上了很难看出是怎么回事。
address = "127.0.0.1:18787"
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
  双击 teldrive-gui.exe  →  等几秒，网页界面会在一个独立窗口里打开：
                            窗口里就是完整的网页界面，没有地址栏、没有标签页，
                            也不是内嵌浏览器——用的是系统里已装的浏览器，
                            只是按「应用窗口」方式打开。
  之后它就待在右下角托盘里：右键图标可以「打开界面 / 复制地址 / 退出 Teldrive」，
  左键单击图标会重新打开那个窗口。
  停止：托盘图标右键 → 退出 Teldrive
        （服务会把随包 PostgreSQL 一起停掉）

  窗口第一次打开时需要登录一次 Telegram，登录状态保存在
  data\browser-profile 里，之后每次双击都是已登录状态。

  想用原来的黑色控制台窗口：双击 teldrive.exe，用法和以前完全一样，
  按 Ctrl+C 或关闭窗口即停止服务。

  两个入口都是自包含的：会自己启动随包的 PostgreSQL，退出时也会自己把
  数据库停掉。teldrive-gui.exe 的日志写在 data\teldrive-gui.log。

  第一次启动会自动生成随机密钥、初始化内置数据库，大约需要 1 分钟；
  之后每次启动在 5 秒左右。

【里面有什么】
  teldrive-gui.exe 推荐入口：双击打开网页窗口，后台只留一个托盘图标（无控制台）
  teldrive.exe     命令行入口：启动数据库、启动服务、打开默认浏览器
  config.toml     配置文件（端口、Telegram api_id、登录白名单等）
  pgsql\          便携版 PostgreSQL 17，绿色免安装，只监听 127.0.0.1:5433
  data\           运行数据：pgdata 数据库、browser-profile、postgres.log、initdb.log
  使用说明.txt    本文件

【首次使用要做的事】
  1. 打开网页后，用你的 Telegram 账号登录（需要手机接收验证码）。
     电话号码必须带国家代码、以 + 开头，例如 +8613800138000；
     只填 138... 会被服务器拒绝。空格和横线随便打，程序会自动整理。
  2. 建议先看页面里的「设置」，确认存储频道等配置。
  3. app-id / app-hash 用的是官方示例值。如果登录时报 API 相关错误，
     到 https://my.telegram.org 申请自己的 api_id / api_hash，
     填进 config.toml 的 [telegram] 段后重启 teldrive.exe。

【界面语言：简繁中日韩随便切】
  顶部右上角（登录页）和「设置 → 外观 → Language」都有语言切换器，
  可选 English / 简体中文 / 繁體中文 / 日本語 / 한국어，选中即时生效并记住。
  首次打开会跟随系统/浏览器语言自动选择。

  注意：请【不要】再用 Chrome 的"翻译此页 / 始终翻译英文"。
  Google 翻译会重写页面 DOM，React 接着更新时就会报
  "Failed to execute 'insertBefore' on 'Node' ..."，表现为按钮点不动。
  现在页面已自带四种语言，不再需要浏览器翻译（页面也声明了 notranslate）。

【重要提醒】
  * 默认只监听 127.0.0.1，只有这台电脑能访问；想在局域网里用手机或平板访问，
    到「设置 → 本地网络访问」打开开关，然后在那台设备上申请、在这台电脑上
    批准。批准前那台设备什么也做不了，批准后它借用的是这台电脑的登录状态。
  * 端口被占用时不用管：程序会自己往后找一个空闲端口（首选 18787）。
    实际端口记在 data\port 里，窗口和设置页都会显示真正的地址。
  * 数据目录（data\）不要删；里面是数据库。换机器时整个文件夹拷走即可。
  * 想彻底重置：删除 data\ 目录后重新双击 teldrive-gui.exe（数据会全部清空）。
  * Telegram 对 API 调用频率有严格限制，请勿滥用，否则账号可能被限制。

【常见问题】
  启动失败：
      teldrive-gui.exe 会弹一个错误框说明原因；数据库细节看 data\initdb.log、
      data\postgres.log，程序自己的记录看 data\teldrive-gui.log。
  双击后没有新窗口：
      说明它已经在运行了（托盘里有图标），双击只会把网页窗口重新打开，
      不会启动第二个服务。托盘图标右键 →「打开界面」是同一个动作。
  想用命令行启动（例如接外置 PostgreSQL）：
      teldrive.exe run -c config.toml

【本包是怎么构建的】
  源码：teldrive-2（github.com/tgdrive/teldrive v2）
  构建：Go + bun 构建前端 → 前端资源嵌入两个 exe；CI 在 windows-latest 上打包
  数据库：便携 PostgreSQL 17.11（官方 Windows x64 构建，免安装）
  本包的改动（v2.5.0）：
    1) 双击即用（teldrive-gui.exe）：用系统已装的浏览器开一个「应用窗口」显示
        完整网页界面（无地址栏、无标签页，不是内嵌 WebView），后台只留托盘图标
        「打开界面 / 复制地址 / 退出」；原来的控制台入口 teldrive.exe 完全保留；
    2) 登录「保持登录」开关：默认记住登录，可以关掉改成每次重新登录；
    3) 便携密钥保管：signing-key / data-key / 数据库地址 / 加密密钥用
        AES-256-GCM + PBKDF2(SHA-256, 60 万次) 封存在本机；口令可自设或随机
        生成（14–25 位），默认记在本机，也可改成每次启动手填；
    4) 备份包：可选密码加密（同样 AES-256-GCM + PBKDF2），备份内容可勾选
        （用户、加密密钥、上传密钥），恢复是手动按钮，可选覆盖或合并；
    5) 端口不再固定 8080：默认 18787，被占用时自动往后找空闲端口；
    6) 局域网访问申请制：客户端申请、电脑端批准，可随时撤销；
    7) 设置页显示「其他设备打开哪个地址」，可以一键复制。
  更早的改动（v2.0.3）：
    1) 页面声明 notranslate，避免浏览器整页翻译把 React 页面弄崩；
    2) 登录页手机号自动规范化（去空格/横线、00 转 +），格式不对时给红色提示；
    3) 服务端 400 由"request could not be decoded"改为指出具体字段与原因；
    4) 内置简繁中日韩界面（无需浏览器翻译）；
    5) 静态资源构建期 brotli 预压缩，首屏传输约 2MB → 500KB；
    6) 上传稳定性加固（分片重试与卡死超时、会话自愈、修复冲突策略设置导致上传必失败）；
    7) 修复浅色主题（此前切换后界面仍为深色）并修正对比度；
    8) WebDAV 支持（在「设置 → WebDAV」里开启，用 API 密钥作为密码）；
    9) 启动方式统一：入口不再是 .bat 脚本，双击即可。
========================================================================
"""

os.makedirs(BUNDLE, exist_ok=True)

# `--public` produces a distributable bundle: config.toml carries placeholders
# instead of this machine's real keys, and teldrive.exe generates fresh random
# keys on first run. Never ship a bundle that contains someone's live secrets.
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


def write_utf8_bom(name, text):
    path = os.path.join(BUNDLE, name)
    with open(path, "wb") as fh:
        fh.write(b"\xef\xbb\xbf" + text.replace("\n", "\r\n").encode("utf-8"))
    print("wrote", name)


# Retired launcher scripts: an older bundle shipped start.bat / stop.bat and two
# underscore-prefixed helpers. Removing them here also removes them from an
# already-populated bundle directory, so the release cannot ship both entries.
for retired in ("start.bat", "stop.bat", "_open-browser.bat", "_init-keys.bat"):
    retired_path = os.path.join(BUNDLE, retired)
    if os.path.exists(retired_path):
        os.remove(retired_path)
        print("removed retired script:", retired)

write_utf8_bom("使用说明.txt", README)
print("bundle files ready:", BUNDLE)
