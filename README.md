# Teldrive Portable

**Windows 上双击即用的便携网盘。** 自带数据库和网页界面，把 Telegram 变成自己的网盘；再加一层「局域网伴侣访问」——手机、平板可以接进来用，但让不让进，由这一台电脑说了算。

[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
![Platform](https://img.shields.io/badge/platform-Windows%2010%2F11-0078D6.svg)
[![Release](https://img.shields.io/github/v/release/w32394045-dotcom/teldrive-portable?label=release)](https://github.com/w32394045-dotcom/teldrive-portable/releases)

---

## 这是什么

一个解压即用的绿色包：不需要安装 PostgreSQL、不需要 Node、不需要管理员权限。

双击 `teldrive-gui.exe`，几秒后网页界面会在一个应用窗口里打开——窗口里就是完整的网页界面，没有地址栏和标签页（用系统里已装的浏览器按应用模式打开，不是内嵌 WebView）。之后它待右下角托盘里：左键重开窗口，右键可以打开界面、复制地址、退出。

数据全部在包目录里（数据库、浏览器登录状态、备份设置、金库），换机器时整个文件夹拷走即可。

## 功能

| 功能 | 它做了什么 |
| --- | --- |
| **双击即用** | 首次运行自动生成随机密钥、初始化内置 PostgreSQL（只听 `127.0.0.1:5433`）、建库、起服务、开窗口。退出时只停掉自己启动的数据库。 |
| **托盘常驻** | 关掉窗口服务照跑；托盘菜单可重开界面、复制地址、退出；有设备申请访问时弹气泡提醒。 |
| **局域网伴侣访问** | 在「设置 → 局域网访问」打开后，同一网络里的设备打开地址会看到申请页：客户端申请 → 电脑端逐台批准 → 设备借用这台电脑的登录状态，看到同一份文件。可随时撤销。 |
| **设置加密（金库）** | 把 signing-key、data-key、数据库地址、加密密钥用 **AES-256-GCM + PBKDF2(SHA-256, 60 万次)** 封在本地。口令可自设或随机生成（14–25 位）；默认记在本机（双击即启动），也可改成每次启动手填。 |
| **备份与恢复** | 快照传到独立频道，内容可勾选（用户、加密密钥；金库密钥随包带上，换机可读）；可选密码加密（同样 AES-256-GCM + PBKDF2 60 万次，密码可留空）。恢复是手动按钮 + 输入确认，可选覆盖或合并。 |
| **保存到这台电脑** | 文件工具栏「保存到这台电脑」把文件写进你选定的目录，而不是浏览器自己的下载目录；「每次询问」打开则每次先弹系统文件夹选择框。 |
| **保持登录** | 登录页开关，默认开：关掉浏览器再回来不用重新登录 Telegram。 |
| **端口不写死** | 默认 18787，被占用就自动往后找空闲端口；实际端口记在 `data/port`，窗口、设置页、托盘显示的都是真地址。 |
| **WebDAV / 挂载盘 / 开机自启** | 可给文件管理器和 rclone 用；一键把网盘映射成盘符；开机自启时静默进托盘，不弹窗不开浏览器。 |
| **五种界面语言** | 简体中文 / 繁體中文 / English / 日本語 / 한국어，运行时切换，跟随系统语言；深浅色主题都补齐了对比度。 |

## 快速开始

1. 到 [Releases](https://github.com/w32394045-dotcom/teldrive-portable/releases) 下载最新的 `teldrive-2-win64.zip`；
2. 解压到任意目录（**不要**放在需要管理员权限的位置，例如 `C:\Program Files`）；
3. 双击 **`teldrive-gui.exe`**；
4. 首次启动约 1 分钟（初始化数据库），之后 5 秒左右；网页界面自己会弹出来。

要停止：托盘图标右键 →「退出 Teldrive」，服务会把内置数据库一起停掉。

想用原来的控制台方式（调试、接外置 PostgreSQL）：

```
teldrive.exe run -c config.toml    前台运行，Ctrl+C 停止
teldrive.exe check -c config.toml  只校验配置与依赖
teldrive.exe version               查看版本
```

### 第一次启动会做什么

1. 生成随机 `signing-key` / `data-key` 写回 `config.toml`（发行包里是占位符，不会带上别人的密钥）；
2. `initdb` 初始化内置 PostgreSQL，并用单模式建好 `teldrive` 库；
3. `pg_ctl` 启动数据库，`127.0.0.1:5433` 起服务；
4. 等接口就绪后打开界面窗口。

`teldrive-gui.exe` 没有控制台，诊断信息写在 `data/teldrive-gui.log`；控制台入口和数据库日志分别是窗口输出、`data/initdb.log`、`data/postgres.log`。

## 设置在哪

| 页面 | 内容 |
| --- | --- |
| 设置 → 备份 | 快照计划、备份内容勾选、加密密码、手动恢复 |
| 设置 → 设置加密 | 金库：创建/更换口令、保存方式（本机记住 / 每次手填）、忘记 |
| 设置 → 局域网访问 | 网络开关、待批准设备、已批准设备撤销、其他设备该打开的地址 |
| 设置 → WebDAV | WebDAV 开关、开机自启、挂载网络盘、**保存到这台电脑**（目录 + 每次询问） |
| 设置 → API 密钥 / 会话 / 用户与角色 | 访问凭据与账号 |
| 设置 → 外观 | 语言、主题 |

## 几个关键设计

- **便携是硬约束。** 两个 exe 都是 `CGO_ENABLED=0` 的静态二进制，包目录内一切可搬走：数据库在 `data/pgdata`，浏览器登录状态在 `data/browser-profile`，设置在 `data/*.json`。
- **能用浏览器，就不造浏览器。** 界面只做一份网页。要「像个应用」时用浏览器自己的应用窗口（`--app=`），不用 WebView 套壳——省掉一个渲染引擎、一份安装依赖，也不用维护两套界面。
- **敏感的东西默认不出机器。** 金库文件用口令封、备份包可选密码封，两者都是 AES-256-GCM + PBKDF2 60 万次；设备凭据只存 SHA-256 哈希；恢复这种破坏性操作必须手动确认。
- **局域网不是「打开端口」而是「逐台批准」。** 局域网里能连上端口不代表能读文件：未批准的设备只能看到申请页；批准后借用的是电脑端会话，撤销立即生效。
- **下载目录是网页改不了的。** 浏览器自己的下载目录页面无权修改，所以「保存到这台电脑」走服务端：文件由服务端写盘，目录来自系统文件夹选择框，重名编号而不是覆盖，写一半失败会删掉残文件。

## 从源码构建

需要 **Go ≥ 1.26**、**bun**，以及一份便携 PostgreSQL 17 的 Windows 二进制。

```bash
# 1. 前端（产物含 brotli 预压缩副本）
cd src/teldrive-2/ui && bun install --frozen-lockfile && bun run build

# 2. 服务端 + 原生壳
cd src/teldrive-2
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=v2.5.0 -X main.commit=$(git rev-parse --short HEAD) -X main.date=$(date -u +%FT%TZ)" \
  -o ../../dist/teldrive-2-win64/teldrive.exe ./cmd/teldrive
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -H=windowsgui -X main.version=v2.5.0 -X main.commit=$(git rev-parse --short HEAD) -X main.date=$(date -u +%FT%TZ)" \
  -o ../../dist/teldrive-2-win64/teldrive-gui.exe ./cmd/teldrive-gui

# 3. 生成 config.toml / 使用说明，并打 zip（--public 写密钥占位符，供发行）
cd ../..
python tools/make_bundle.py dist/teldrive-2-win64 --public
python tools/make_zip.py   dist/teldrive-2-win64 dist/teldrive-2-win64.zip
python tools/verify_bundle.py dist/teldrive-2-win64.zip   # 发布闸门
```

推 `v*` 标签会触发 [release.yml](.github/workflows/release.yml) 在 `windows-latest` 上构建两个 exe、组装便携包、跑发布闸门并上传资产。

## 仓库结构

| 路径 | 内容 |
| --- | --- |
| `src/teldrive-2/cmd/teldrive-gui` | 原生壳：托盘、应用窗口、开机自启时的静默启动 |
| `src/teldrive-2/cmd/teldrive` | 控制台入口 + 便携包启动器（密钥生成、内置 PG、端口探测、打开浏览器） |
| `src/teldrive-2/internal/desktop` | 托盘、原生对话框（口令框、文件夹选择）、浏览器应用窗口、本机下载设置 |
| `src/teldrive-2/internal/bundled` | 便携启动：`Detect`/`EnsureKeys`/`SelectAddress`/`Launcher` |
| `src/teldrive-2/internal/vault` | 设置金库（封存、口令模式、生成/校验口令） |
| `src/teldrive-2/internal/backup` | 快照索引、AES-GCM 封装、内容勾选、恢复（覆盖/合并） |
| `src/teldrive-2/internal/lanaccess` | 局域网设备注册表（申请、批准、撤销、令牌哈希） |
| `src/teldrive-2/internal/webdav` | WebDAV 服务（权限复用 catalog/uploads/transfer） |
| `src/teldrive-2/internal/app` | 组装根；额外 HTTP 面（backup/vault/lan/desktop/system） |
| `src/teldrive-2/internal/api` | 由 TypeSpec 生成的 OpenAPI 处理器 |
| `src/teldrive-2/ui` | React 19 前端（TanStack Router/Query、HeroUI、运行时 i18n） |
| `tools/` | 打包、校验、端到端与审计脚本 |

## 测试与发布闸门

```bash
cd src/teldrive-2
go vet ./internal/... ./cmd/...        # 静态检查
go test -count=1 -short ./...          # 37 个包的单元测试
cd ui && node node_modules/typescript/bin/tsc   # 前端类型检查
```

`tools/verify_bundle.py` 是发布闸门：必需文件齐全、**不含**运行数据（`data/`、`pgdata`、日志）、**不含**真实密钥（必须是占位符）、**不含**任何 `.bat` 启动脚本。任何一项不满足，发布直接失败。

## 与上游 teldrive 的关系

这个项目起于 [tgdrive/teldrive](https://github.com/tgdrive/teldrive)（MIT），沿用了它的网页界面与服务端骨架；`src/teldrive-2/` 就是那份源码，随仓库保留以便对照与再合并。

在此之上，本仓库重写或新增了：

- **启动与打包**：便携包启动器（自动生成密钥、自带 PostgreSQL、端口探测、`data/port`、退出只停自己启动的库）、`teldrive-gui.exe` 原生壳、`tools/` 里的全部打包与校验脚本；
- **原生壳**：托盘、浏览器应用窗口、开机自启静默模式、原生口令框与文件夹选择框；
- **安全与备份**：设置金库、备份加密封装与内容勾选、手动恢复、金库密钥随包；
- **局域网**：申请制设备授权、设备借用电脑端会话、待批准提醒、局域网地址展示与「保存到这台电脑」；
- **WebDAV**：可读写的 WebDAV 面 + 设置页 + 挂载网络盘 + 前置条件检测与提权修复；
- **界面**：五种语言的运行时 i18n、浅色主题修复、禁用浏览器整页翻译、登录手机号规范化、更准确的 400 文案；
- **性能与稳定性**：构建期 brotli 预压缩（首屏 ≈2 MB → ≈500 KB）、路由级代码分割、上传分片重试/超时/会话自愈与冲突策略修复。

## 许可

MIT。上游版权声明保留在 [LICENSE](LICENSE)（Copyright (c) 2024 divyam234）；`tools/` 与本仓库新增的代码同样以 MIT 发布。
