//go:build windows

// Command teldrive-gui starts the portable drive the way people expect an
// application to start: double-click it and the drive opens.
//
// The drive is a web application, so "opens" means the page appears in the
// browser already installed on the machine. Nothing is embedded here - no
// WebView, no second interface - which is why this program has no window of its
// own. All it leaves behind is a tray icon: the one thing a windowless program
// needs in order to be visible, stoppable, and able to reopen the page.
//
// It is built with -H=windowsgui, so there is no console; everything that would
// have been printed goes to data/teldrive-gui.log.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/spf13/pflag"

	"github.com/tgdrive/teldrive/v2/internal/app"
	"github.com/tgdrive/teldrive/v2/internal/bundled"
	"github.com/tgdrive/teldrive/v2/internal/config"
	"github.com/tgdrive/teldrive/v2/internal/desktop"
	"github.com/tgdrive/teldrive/v2/internal/logging"
	"github.com/tgdrive/teldrive/v2/internal/vault"
)

// These are set from the same -ldflags the server binary uses, so a window and
// a log line always agree with what was built.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

const (
	// pollInterval is how often the status endpoint is asked what is going on.
	// It is a local HTTP call, but there is no reason to make it a busy loop.
	pollInterval = 2 * time.Second
	// shutdownWait bounds how long the window waits for the server to close.
	shutdownWait = 10 * time.Second
	// requestTimeout keeps one unreachable endpoint from stalling the poller.
	requestTimeout = 3 * time.Second
)

func main() {
	// The window, the clipboard and the message queue all belong to one thread,
	// and Go's main goroutine is the only one that starts there.
	runtime.LockOSThread()
	if err := run(); err != nil {
		desktop.Error("Teldrive", err.Error())
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "配置文件的位置；默认使用便携包里的 config.toml")
	// The autostart entry the settings page writes passes this flag, so the login
	// launch is the same executable without a window or a browser.
	autostart := flag.Bool("autostart", false, "开机自启：只留托盘，不弹窗口、不打开浏览器")
	flag.Parse()

	executable, err := os.Executable()
	if err != nil {
		return err
	}
	layout, inBundle := bundled.Detect(filepath.Dir(executable))
	if *configPath == "" && !inBundle {
		return errors.New("这个程序需要在便携包目录里运行：同目录下应该有 config.toml 和 pgsql\\bin。\n" +
			"也可以用 -config 指定一个配置文件。")
	}

	if inBundle {
		// Relative paths in config.toml resolve against the working directory, so
		// the bundle root has to be it - exactly as the console launcher does.
		if err := os.Chdir(layout.Root); err != nil {
			return fmt.Errorf("进入便携包目录失败: %w", err)
		}
		if _, err := bundled.EnsureKeys(layout.ConfigPath); err != nil {
			return err
		}
	}
	if *configPath == "" {
		*configPath = layout.ConfigPath
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}

	logger, closeLog, err := newLogger(cfg, layout, *configPath)
	if err != nil {
		return err
	}
	defer closeLog()

	// The vault comes first: it may hold the database URL the bundled PostgreSQL
	// has to be started from.
	unlocked, err := unlock(&cfg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	state := &state{}
	serverDone := make(chan struct{})
	close(serverDone)

	// The window that shows the drive is the browser's own app mode, with a
	// profile of the drive's own: the login survives restarts and the person's
	// normal browsing is never touched.
	profileDir := filepath.Join(dataDirOf(layout, *configPath), "browser-profile")
	open := func(url string) error { return openWindow(url, profileDir, logger) }

	address := ""
	startedHere := false
	if inBundle && *configPath == layout.ConfigPath {
		launcher := bundled.New(layout, io.Discard, logger)
		launcher.SetDatabase(cfg.Database.URL)
		launcher.SetHTTPAddress(cfg.HTTP.Address)
		if running := launcher.RunningAddress(ctx); running != "" {
			// A second double-click must open the drive that is already running:
			// starting a rival server, or a second tray icon, is never what the
			// person meant. The port has moved before, which is why the launcher,
			// not the configuration file, answers where the drive is.
			if *autostart {
				return nil
			}
			return open(running)
		}
		selected, err := launcher.SelectAddress(ctx)
		if err != nil {
			return err
		}
		cfg.HTTP.Address = selected
		address = launcher.ServerURL()
		if err := launcher.Prepare(ctx); err != nil {
			return err
		}
		defer launcher.StopPostgres()
		startedHere = true
	} else {
		address = siteURL(cfg.HTTP.Address)
		startedHere = true
	}
	state.setAddress(address)

	if startedHere {
		application, err := app.New(ctx, cfg, app.Dependencies{
			Logger:   logger,
			Version:  version,
			VaultKey: unlocked.Key,
			Desktop:  shellBridge{},
		})
		if err != nil {
			return fmt.Errorf("初始化 Teldrive 失败: %w", err)
		}
		logger.Info("desktop.starting", "address", cfg.HTTP.Address, "version", version, "commit", commit, "date", date)
		serverDone = make(chan struct{})
		go func() {
			defer close(serverDone)
			if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("desktop.stopped", "error", err)
				state.setDetail("服务已停止：" + err.Error())
				return
			}
			logger.Info("desktop.stopped")
		}()
	}

	go poll(ctx, state, logger, !*autostart, open)

	runErr := desktop.Run(desktop.Callbacks{
		Title:  "Teldrive " + version,
		Status: state.status,
		OpenBrowser: func() error {
			return open(state.addressValue())
		},
		Copy: desktop.CopyToClipboard,
		Quit: cancel,
		Log:  func(msg string, args ...any) { logger.Info(msg, args...) },
	})

	// The tray is gone: close the server, then wait for it to finish rather than
	// pulling the process out from under an upload in progress.
	cancel()
	select {
	case <-serverDone:
	case <-time.After(shutdownWait):
		logger.Warn("desktop.shutdown_timeout")
	}
	return runErr
}

// shellBridge is what the server is allowed to borrow from the shell. There is
// exactly one thing today: a folder picker, which only a person sitting in front
// of this computer can answer.
type shellBridge struct{}

func (shellBridge) ChooseFolder(title, initial string) (string, bool) {
	return desktop.ChooseFolder(title, initial)
}

// state is what the tray reports, written by the poller and read by the UI
// thread. Every field is behind the mutex because those are two goroutines.
type state struct {
	mu      sync.Mutex
	address string
	network string
	pending int
	detail  string
	opened  bool
}

func (s *state) setAddress(address string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.address = address
}

func (s *state) addressValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.address
}

func (s *state) setDetail(detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.detail = detail
}

func (s *state) set(address, network string, pending int, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if address != "" {
		s.address = address
	}
	s.network = network
	s.pending = pending
	s.detail = detail
}

func (s *state) status() desktop.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return desktop.Status{
		Address:        s.address,
		NetworkAddress: s.network,
		Pending:        s.pending,
		Detail:         s.detail,
	}
}

// statusPayload mirrors the fields of GET /api/system/status that the tray
// reflects. It is read over HTTP rather than out of the running application: the
// endpoint is the same one the settings page uses, so there is one answer to
// "where is this drive", not two.
type statusPayload struct {
	URL            string `json:"url"`
	NetworkURL     string `json:"networkUrl"`
	NetworkEnabled bool   `json:"networkEnabled"`
	BoundToNetwork bool   `json:"boundToNetwork"`
	PendingDevices int    `json:"pendingDevices"`
}

func poll(ctx context.Context, state *state, logger *slog.Logger, openBrowser bool, open func(string) error) {
	client := &http.Client{Timeout: requestTimeout}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	endpoint := strings.TrimSuffix(state.addressValue(), "/") + "/api/system/status"

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			state.setDetail("正在等待服务启动…")
			continue
		}
		var payload statusPayload
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 1<<16)).Decode(&payload)
		response.Body.Close()
		if decodeErr != nil {
			logger.Warn("desktop.status_decode", "error", decodeErr)
			continue
		}
		state.set(payload.URL, payload.NetworkURL, payload.PendingDevices, describe(payload))
		if openBrowser {
			openOnce(state, payload.URL, open, logger)
		}
	}
}

// describe is the one line under the addresses: what version is running and
// whether the network can reach it.
func describe(payload statusPayload) string {
	parts := []string{fmt.Sprintf("版本 %s · %s", version, commit)}
	switch {
	case payload.NetworkEnabled && payload.BoundToNetwork:
		parts = append(parts, "局域网访问已开启")
	case payload.NetworkEnabled:
		parts = append(parts, "局域网访问将在下次启动后生效")
	default:
		parts = append(parts, "仅本机可以访问")
	}
	if payload.PendingDevices > 0 {
		parts = append(parts, "有设备等待批准")
	}
	return strings.Join(parts, " · ")
}

// openOnce opens the drive the first time it answers, which is what a
// double-click has always done. It happens after the endpoint replies rather
// than on a timer, so the window is never opened at a port nothing listens on.
func openOnce(state *state, url string, open func(string) error, logger *slog.Logger) {
	state.mu.Lock()
	already := state.opened
	state.opened = true
	state.mu.Unlock()
	if already || url == "" {
		return
	}
	if err := open(url); err != nil {
		logger.Warn("desktop.open_window", "error", err)
	}
}

// openWindow shows the drive in an app-mode window of the installed browser: the
// page and nothing else, no tabs, no address bar, and no embedded engine. A
// machine without a Chromium-based browser still gets the page, in a normal tab.
func openWindow(url, profileDir string, logger *slog.Logger) error {
	if url == "" {
		return errors.New("the drive is not ready yet")
	}
	if err := desktop.OpenAppWindow(url, profileDir); err != nil {
		logger.Warn("desktop.app_window", "error", err)
		return bundled.OpenBrowser(url)
	}
	return nil
}

// unlock opens the settings vault. When the vault was sealed in prompt mode the
// passphrase is asked for in a native box; a rejected key is not fatal, because
// a typo is the likeliest reason for one and exiting over it would be cruel.
func unlock(cfg *config.Config) (app.Unlocked, error) {
	ctx := context.Background()
	if !app.VaultNeedsKey() {
		return app.UnlockVault(ctx, cfg, nil)
	}
	for {
		key, ok := desktop.Prompt("Teldrive",
			"这台机器上的设置是加密保存的，请输入打开它的口令。", true)
		if !ok {
			return app.Unlocked{}, errors.New("没有口令就无法打开已加密的设置，程序退出。")
		}
		unlocked, err := app.UnlockVaultWithKey(ctx, cfg, key, nil)
		if err == nil {
			return unlocked, nil
		}
		if errors.Is(err, vault.ErrKeyRejected) {
			desktop.Error("Teldrive", "口令不对，请再试一次。")
			continue
		}
		return unlocked, err
	}
}

// loadConfig reads the configuration through the same loader the command line
// uses, so every option keeps working.
func loadConfig(path string) (config.Config, error) {
	flags := pflag.NewFlagSet("teldrive", pflag.ContinueOnError)
	loader := config.NewLoader()
	loader.RegisterFlags(flags)
	if err := flags.Set("config", path); err != nil {
		return config.Config{}, err
	}
	return loader.Load(flags)
}

// newLogger writes to a file beside the data, because a windowed program has no
// console to print diagnostics to.
func newLogger(cfg config.Config, layout bundled.Layout, configPath string) (*slog.Logger, func(), error) {
	path := filepath.Join(dataDirOf(layout, configPath), "teldrive-gui.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("无法写入日志 %s: %w", path, err)
	}
	logger, err := logging.NewLogger(file, cfg.Logging.LogLevel, cfg.Logging.LogFormat)
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return logger, func() { file.Close() }, nil
}

// dataDirOf is the directory that belongs to this installation: the bundle's
// data\ when running from a bundle, and the configuration file's own folder
// otherwise. The log and the browser profile both live there, so nothing is
// written outside the folder a person can copy to another machine.
func dataDirOf(layout bundled.Layout, configPath string) string {
	if layout.DataDir != "" {
		return layout.DataDir
	}
	return filepath.Dir(configPath)
}

// siteURL turns a listen address into something a browser can open. A wildcard
// bind is not an address anybody can connect to, so it becomes the loopback one.
func siteURL(address string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return "http://" + strings.TrimSpace(address) + "/"
	}
	switch strings.ToLower(strings.Trim(host, "[]")) {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}
