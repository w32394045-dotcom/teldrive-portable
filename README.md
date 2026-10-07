# teldrive-portable

[![teldrive](https://img.shields.io/badge/upstream-tgdrive%2Fteldrive-blue)](https://github.com/tgdrive/teldrive)

把 **teldrive v2** 做成 **Windows 双击就能跑**的绿色包，并补上 **简繁中日韩（zh-CN / zh-TW / ja / ko）** 界面。

## 怎么用（只有一个入口）

1. 到 [Releases](https://github.com/w32394045-dotcom/teldrive-portable/releases) 下载最新的 `teldrive-2-win64.zip`；
2. 解压到任意目录（**不要**放在需要管理员权限的目录，例如 `C:\Program Files`）；
3. **双击 `teldrive.exe`**，等几秒浏览器自动打开 <http://127.0.0.1:8080>。

停止：关闭那个黑色窗口，或在窗口里按 `Ctrl+C`。

### 为什么只有 exe，没有 .bat

包里的入口就是 `teldrive.exe` 一个文件。它自己完成全部启动流程：

1. 首次运行生成随机 `signing-key` / `data-key` 写回 `config.toml`（发行包里是占位符，不会带上别人的密钥）；
2. 首次运行 `initdb` 初始化内置 PostgreSQL，并用单用户模式建好 `teldrive` 库；
3. 以 `pg_ctl` 启动内置数据库（只监听 `127.0.0.1:5433`）；
4. 启动服务并在端口就绪后打开浏览器；
5. 退出时**只停掉它自己启动的**数据库，外置 PostgreSQL 不会被碰。

早期版本同时提供 `start.bat` / `stop.bat`，导致"到底该双击哪个"的困惑；这些脚本已经移除，发布校验（`tools/verify_bundle.py`）会在包里发现任何启动脚本时**直接让发布失败**。

命令行仍然可用（要接外置 PostgreSQL 时用得上）：

```
teldrive.exe run -c config.toml    前台运行服务
teldrive.exe check -c config.toml  只校验配置与依赖
teldrive.exe version               查看版本
```

## 包内容

| 文件 | 作用 |
| --- | --- |
| `teldrive.exe` | **唯一入口**：启动数据库、启动服务、打开浏览器（前端资源已 embed 进二进制） |
| `config.toml` | 配置（端口、Telegram `api_id`、登录白名单等）；发行版内含密钥占位符 |
| `pgsql/` | 便携 PostgreSQL 17（免安装、只听 `127.0.0.1:5433`） |
| `data/` | 运行数据（`pgdata`、`postgres.log`、`initdb.log`），首次运行自动创建 |
| `使用说明.txt` | 面向最终用户的中文说明（UTF-8 BOM，记事本可直接打开） |

teldrive v2 强依赖 PostgreSQL 与 River 后台任务，所以"一键"的关键是把 PG 也带上：`initdb -A trust --encoding=UTF8 --locale=C` 初始化，`postgres --single` 建库，`pg_ctl -w start` 等就绪。这些现在都在 exe 里，见 [`internal/bundled`](src/teldrive-2/internal/bundled)。

## WebDAV

在 **设置 → WebDAV** 打开后，可用文件管理器或 rclone 挂载：

- 地址：`http://127.0.0.1:8080/webdav`
- 认证：HTTP Basic；**用户名随意，密码填一个 API 密钥**（设置 → API 密钥 里创建）

支持浏览、下载、上传、新建文件夹、重命名、删除；删除是移入回收站（可恢复，不会物理清除）。暂不支持锁定（LOCK）。默认关闭，开关状态存在 `data/webdav.json`，改动立即生效、无需重启。

## 本仓库相对上游的改动

1. **启动方式统一**：只有 `teldrive.exe` 一个入口（原先还有 `start.bat` / `stop.bat` / 两个内部 bat）。
2. **WebDAV 支持**：`internal/webdav`（可读写，权限复用 catalog/uploads/transfer 服务）+ 设置页开关。
3. **前端禁用浏览器整页翻译**（`ui/index.html`：`translate="no"` + `<meta name="google" content="notranslate">`）。
   Google 翻译会重排 React 管理的 DOM，随后任何一次提交都会抛
   `Failed to execute 'insertBefore' on 'Node' ...`，表现为按钮点不动、页面卡死。
4. **登录页手机号规范化**（`ui/src/routes/login.tsx`）：自动去掉空格/横线/括号、`00` 前缀转 `+`，格式不合法时内联红字提示并禁用按钮。
5. **服务端 400 文案更准确**（`internal/api/support.go`）：schema 校验失败不再谎报 `request could not be decoded`，改为指出具体字段与原因。
6. **运行时 i18n**（`ui/src/i18n/`）：以英文原文为 key，附 `zh-CN` / `zh-TW` / `ja` / `ko` 译文；语言切换器位于「设置 → 外观」和登录页；切换时同步更新 `<html lang>`。复数消息与界面里原本漏出的枚举值（任务状态、文件类型、用户角色等）也一并处理。
7. **性能**：构建期把静态资源预压缩成 brotli 副本（`vite.config.mts` 的 precompress 插件），Go 端按 `Accept-Encoding` 直接回传，首屏传输约 2 MB → 500 KB（入口 JS 353→97 KB、CSS 480→36 KB），压缩零运行时开销；同时开启路由级代码分割。
8. **浅色主题修复**：此前 `index.html` 硬编码 `data-theme="dark"` 而 next-themes 只写 `class`，切换浅色后界面仍是深色；现在两个属性一起写入，并修正浅色下的文字对比度。
9. **上传稳定性**：修复"冲突策略"选项写入了非法枚举（`error`）导致每次上传都 400 的问题，并补上分片重试/退避/卡死超时、会话自愈、暂停恢复竞态，服务端瞬时错误改为带抖动的退避重试。

## 从源码构建

需要 **Go ≥ 1.26**、**bun**，以及一份便携 PostgreSQL 17 的 Windows 二进制。

```bash
# 1. 前端（产物已含 .br 预压缩副本）
cd src/teldrive-2/ui && bun install --frozen-lockfile && bun run build

# 2. 服务端（前端 dist 会被 embed）
cd src/teldrive-2 && CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=2.0.3 -X main.commit=portable -X main.date=$(date -u +%FT%TZ)" \
  -o ../../dist/teldrive-2-win64/teldrive.exe ./cmd/teldrive

# 3. 生成 config.toml / 使用说明，并打 zip（--public 写入密钥占位符，供发行）
python tools/make_bundle.py dist/teldrive-2-win64 --public
python tools/make_zip.py   dist/teldrive-2-win64 dist/teldrive-2-win64.zip
python tools/verify_bundle.py dist/teldrive-2-win64.zip   # 发布闸门
```

推 `v*` 标签会触发 [release.yml](.github/workflows/release.yml) 在 `windows-latest` 上构建、校验并发布资产。

`tools/` 里的脚本：

| 脚本 | 用途 |
| --- | --- |
| `make_bundle.py` | 生成 `config.toml`（`--public` 写占位符）与 `使用说明.txt`，并清理已废弃的启动脚本 |
| `make_zip.py` | 打包 zip（写入 UTF-8 文件名标志，避免中文文件名乱码） |
| `verify_bundle.py` | 发布闸门：必需文件、禁止携带运行数据/真实密钥、**禁止出现启动脚本** |
| `e2e-bundle.py` | 端到端验证"只有 exe"：在临时目录组装真实包，用无参数启动，检查密钥生成、数据库启停、brotli 协商 |
| `perf-audit.mjs` | 首屏传输量与时间基线 |
| `ui-audit.mjs` / `ui-audit-theme.mjs` | 多语言 × 多视口的显示与对比度审计 |
| `verify-display-i18n.mjs` | 断言界面不再漏出未翻译的枚举与英文复数 |
| `verify-upload-settings.mjs` | 断言非法的上传设置会被纠正（否则每次上传都 400） |
| `nfetch.mjs` | 用 Node 的 TLS 下载（本机 curl/schannel 不可用时） |

## 许可

`src/teldrive-2/` 是 [tgdrive/teldrive](https://github.com/tgdrive/teldrive) 的副本，
遵循上游的 **MIT License**（见 [LICENSE](LICENSE)，Copyright (c) 2024 divyam234）。
本仓库新增的打包脚本（`tools/`）、WebDAV 与 i18n 补丁同样以 MIT 发布。

> `tools/` 里的脚本是本机开发与校验用的，默认按仓库位置推算目录；
> 个别脚本（`ui-audit*.mjs` / `verify-*.mjs`）仍需按你的环境调整 Chrome 可执行文件路径。
