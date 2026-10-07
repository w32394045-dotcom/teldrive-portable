# teldrive-portable

[![teldrive](https://img.shields.io/badge/upstream-tgdrive%2Fteldrive-blue)](https://github.com/tgdrive/teldrive)

把 **teldrive v2** 做成 **Windows 双击就能跑**的绿色包，并补上 **简繁中日韩（zh-CN / zh-TW / ja / ko）** 界面。

## 目录结构

```
src/teldrive-2/     teldrive v2 源码（含本仓库的补丁）
tools/              打包与验证脚本（Node/Python，不需要安装依赖）
dist/               发行产物（已在 .gitignore 中，由脚本生成，不入库）
```

## 一键包是什么

`dist/teldrive-2-win64/`（或同名 zip）解压后：

| 文件 | 作用 |
| --- | --- |
| `start.bat` | 一键启动：首次自动 `initdb` 建库 → 起内置 PostgreSQL → 起 teldrive → 等端口就绪后开浏览器 |
| `stop.bat` | 一键停止服务与数据库 |
| `teldrive.exe` | 服务主程序，前端资源已 embed 进二进制 |
| `config.toml` | 配置（首次由打包脚本生成，含随机 `signing-key` / `data-key`） |
| `pgsql/` | 便携 PostgreSQL 17（免安装、只听 127.0.0.1:5433） |
| `data/` | 运行数据（pgdata、日志），首次启动自动创建 |

teldrive v2 强依赖 PostgreSQL 与 River 后台任务，因此"一键"的关键是把 PG 也一起带上：首次启动用 `initdb -A trust` 初始化，再用 `postgres --single` 建 `teldrive` 库，之后 `pg_ctl -w start` 等就绪。

## 本仓库相对上游的改动

1. **前端禁用了浏览器整页翻译**（`ui/index.html`：`translate="no"` + `<meta name="google" content="notranslate">`）。
   Google 翻译会重排 React 管理的 DOM，随后任何一次提交都会抛
   `Failed to execute 'insertBefore' on 'Node' ...`，表现为按钮点不动、页面卡死。
2. **登录页手机号规范化**（`ui/src/routes/login.tsx`）：自动去掉空格/横线/括号、`00` 前缀转 `+`，格式不合法时内联红字提示并禁用按钮。
3. **服务端 400 文案更准确**（`internal/api/support.go`）：schema 校验失败不再谎报 `request could not be decoded`，改为指出具体字段与原因。
4. **运行时 i18n**（`ui/src/i18n/`）：以英文原文为 key，附 `zh-CN` / `zh-TW` / `ja` / `ko` 译文；语言切换器位于「设置 → 外观」和登录页；切换时同步更新 `<html lang>`。

## 构建

需要 **Go ≥ 1.26**、**bun**（构建前端）以及一份便携 PostgreSQL 17 的 Windows 二进制。

```bash
# 1. 前端
cd src/teldrive-2/ui && bun install --frozen-lockfile && bun run build

# 2. 服务端（前端 dist 会被 embed）
cd src/teldrive-2 && CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=2.0.0 -X main.commit=portable -X main.date=$(date -u +%FT%TZ)" \
  -o ../../dist/teldrive-2-win64/teldrive.exe ./cmd/teldrive

# 3. 生成启动脚本 / 配置 / 使用说明，并打 zip
python tools/make_bundle.py dist/teldrive-2-win64
python tools/make_zip.py   dist/teldrive-2-win64 dist/teldrive-2-win64.zip
```

`tools/` 里的脚本：

| 脚本 | 用途 |
| --- | --- |
| `nfetch.mjs` | 用 Node 的 TLS 下载（本机 curl/schannel 不可用时） |
| `make_bundle.py` | 生成 `start.bat` / `stop.bat` / `config.toml` / `使用说明.txt`（GBK 编码，适配中文控制台） |
| `make_zip.py` | 打包 zip（写入 UTF-8 文件名标志，避免中文文件名乱码） |
| `i18n-scan.mjs` | 用 Babel AST 扫描前端可见文案 |
| `i18n-codemod.mjs` | AST codemod：把文案包成 `t("...")` 并登记 key |
| `verify-*.mjs` | Playwright 验收脚本（登录翻译崩溃复现、i18n 逐语言截图等） |

## 许可

上游 teldrive 的许可同样适用于 `src/teldrive-2/`。打包脚本与补丁部分见仓库作者。
