package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/pflag"

	"github.com/tgdrive/teldrive/v2/internal/app"
	"github.com/tgdrive/teldrive/v2/internal/bundled"
	"github.com/tgdrive/teldrive/v2/internal/config"
	"github.com/tgdrive/teldrive/v2/internal/logging"
)

// pauseEnv disables the interactive pause, for scripted runs and tests.
const pauseEnv = "TELDRIVE_NO_PAUSE"

// errNotBundled means the executable is not sitting inside a released portable
// bundle, so there is no PostgreSQL to manage and nothing to auto-start.
var errNotBundled = errors.New("not a portable bundle")

// launchBundled starts the whole portable distribution from teldrive.exe alone:
// first-run keys, the bundled PostgreSQL, the server, and the browser. This is
// what makes the executable - rather than a batch file - the single entry point.
func launchBundled(ctx context.Context, out io.Writer) error {
	executable, err := os.Executable()
	if err != nil {
		return errNotBundled
	}
	layout, ok := bundled.Detect(filepath.Dir(executable))
	if !ok {
		return errNotBundled
	}
	// Relative paths in config.toml (for example ./data/local-telegram) resolve
	// against the working directory, so the bundle root has to be it.
	if err := os.Chdir(layout.Root); err != nil {
		return fmt.Errorf("enter bundle directory: %w", err)
	}

	generated, err := bundled.EnsureKeys(layout.ConfigPath)
	if err != nil {
		return err
	}
	if generated {
		fmt.Fprintln(out, "      已生成随机 signing-key / data-key")
	}

	cfg, err := loadBundleConfig(layout.ConfigPath)
	if err != nil {
		return err
	}
	logger, err := logging.NewLogger(out, cfg.Logging.LogLevel, cfg.Logging.LogFormat)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	launcher := bundled.New(layout, out, logger)
	launcher.SetDatabase(cfg.Database.URL)
	launcher.SetHTTPAddress(cfg.HTTP.Address)
	serverURL := launcher.ServerURL()

	if launcher.AlreadyRunning(ctx) {
		fmt.Fprintf(out, "Teldrive 已经在运行，正在打开 %s\n", serverURL)
		return bundled.OpenBrowser(serverURL)
	}

	if err := launcher.Prepare(ctx); err != nil {
		return err
	}
	// Only a PostgreSQL this process started is stopped again on exit.
	defer launcher.StopPostgres()

	fmt.Fprintf(out, "[3/3] 启动 Teldrive 服务：%s\n", serverURL)
	fmt.Fprintln(out, "------------------------------------------------------------")
	fmt.Fprintln(out, "  浏览器稍后会自动打开。首次使用请在页面里登录 Telegram。")
	fmt.Fprintln(out, "  按 Ctrl+C 或关闭本窗口即可停止服务。")
	fmt.Fprintln(out, "------------------------------------------------------------")
	go launcher.OpenBrowserWhenReady(ctx, serverURL)

	application, err := app.New(ctx, cfg, app.Dependencies{Logger: logger, Version: buildVersion()})
	if err != nil {
		return fmt.Errorf("initialize TelDrive: %w", err)
	}
	logger.Info("application.starting", "address", cfg.HTTP.Address, "version", buildVersion(), "commit", commit)
	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("application.stopped")
	fmt.Fprintln(out, "Teldrive 已停止。")
	return nil
}

// loadBundleConfig loads the bundle's config.toml through the same loader the
// `run` subcommand uses, so every option keeps working.
func loadBundleConfig(configPath string) (config.Config, error) {
	flags := pflag.NewFlagSet("teldrive", pflag.ContinueOnError)
	loader := config.NewLoader()
	loader.RegisterFlags(flags)
	if err := flags.Set("config", configPath); err != nil {
		return config.Config{}, err
	}
	return loader.Load(flags)
}

// writeLauncherHint documents the command line. It is only reached when the
// executable is not inside a bundle, because inside one the launcher above runs.
func writeLauncherHint(out io.Writer, version string) {
	fmt.Fprintf(out, `Teldrive %s

这个可执行文件不在便携包目录里，因此没有随包的 PostgreSQL 可以自动启动。
便携包请直接双击 teldrive.exe，它会在首次运行时自动生成密钥、初始化并启动
内置数据库，然后打开浏览器。

命令行用法：

  teldrive run -c config.toml    前台运行服务
  teldrive check -c config.toml  只校验配置与依赖
  teldrive version               查看版本
  teldrive --help                查看所有命令
`, version)
}

// pauseWhenDoubleClicked keeps the console window open when the binary was
// launched by double-clicking it (stdin is then the console) so the output above
// can actually be read; Windows closes the window the moment the process exits.
func pauseWhenDoubleClicked(in *os.File, out io.Writer) {
	if os.Getenv(pauseEnv) == "1" {
		return
	}
	info, err := in.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return
	}
	fmt.Fprint(out, "\n按回车键关闭此窗口 ... ")
	_, _ = bufio.NewReader(in).ReadString('\n')
}
