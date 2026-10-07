// Package bundled runs Teldrive straight from the portable Windows release
// layout, where the program owns its own PostgreSQL.
//
// The released folder used to offer two entry points: teldrive.exe plus a set of
// batch scripts, which left every user asking which one they were supposed to
// click. This package moves the whole bring-up sequence (generate first-run
// keys, initialise the bundled PostgreSQL cluster, start it, open the browser,
// stop it again on exit) into the executable itself, so the answer is always
// "double-click teldrive.exe".
package bundled

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// SigningKeyPlaceholder and DataKeyPlaceholder mark a config that has never
	// been started. The distributed config ships them so no two installs share
	// keys; the first run replaces them with fresh randomness.
	SigningKeyPlaceholder = "__SIGNING_KEY__"
	DataKeyPlaceholder    = "__DATA_KEY__"

	signingKeyBytes = 36 // 48 base64 characters
	dataKeyBytes    = 32 // 44 base64 characters

	defaultPGHost = "127.0.0.1"
	defaultPGPort = 5433
	defaultPGUser = "postgres"
	defaultDBName = "teldrive"

	// postgresLogName and initdbLogName land in the bundle's data directory, the
	// same place the previous scripts wrote them.
	postgresLogName = "postgres.log"
	initdbLogName   = "initdb.log"
)

// Layout is the on-disk shape of a released bundle.
type Layout struct {
	Root       string // folder containing teldrive.exe
	ConfigPath string
	PGBin      string // pgsql/bin
	PGData     string // data/pgdata
	DataDir    string // data
}

// Detect reports whether root holds a released bundle, which is what decides
// between "one-click startup" and printing command-line help.
func Detect(root string) (Layout, bool) {
	layout := Layout{
		Root:       root,
		ConfigPath: filepath.Join(root, "config.toml"),
		PGBin:      filepath.Join(root, "pgsql", "bin"),
		PGData:     filepath.Join(root, "data", "pgdata"),
		DataDir:    filepath.Join(root, "data"),
	}
	if !isFile(layout.ConfigPath) || !isFile(layout.postgresBinary()) {
		return Layout{}, false
	}
	return layout, true
}

func (l Layout) postgresBinary() string { return filepath.Join(l.PGBin, binaryName("postgres")) }

func (l Layout) binary(name string) string { return filepath.Join(l.PGBin, binaryName(name)) }

// binaryName adds the Windows extension only where it belongs, so the launcher
// stays buildable and testable on other platforms.
func binaryName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// EnsureKeys replaces the first-run key placeholders in config.toml. It reports
// whether it wrote anything, and refuses to leave a placeholder behind.
func EnsureKeys(configPath string) (bool, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return false, fmt.Errorf("read config: %w", err)
	}
	needsSigning := bytes.Contains(raw, []byte(SigningKeyPlaceholder))
	needsData := bytes.Contains(raw, []byte(DataKeyPlaceholder))
	if !needsSigning && !needsData {
		return false, nil
	}

	secrets := make([]byte, signingKeyBytes+dataKeyBytes)
	if _, err := rand.Read(secrets); err != nil {
		return false, fmt.Errorf("generate keys: %w", err)
	}
	updated := raw
	if needsSigning {
		key := base64.StdEncoding.EncodeToString(secrets[:signingKeyBytes])
		updated = bytes.ReplaceAll(updated, []byte(SigningKeyPlaceholder), []byte(key))
	}
	if needsData {
		key := base64.StdEncoding.EncodeToString(secrets[signingKeyBytes:])
		updated = bytes.ReplaceAll(updated, []byte(DataKeyPlaceholder), []byte(key))
	}
	if bytes.Contains(updated, []byte(SigningKeyPlaceholder)) || bytes.Contains(updated, []byte(DataKeyPlaceholder)) {
		return false, fmt.Errorf("config still contains key placeholders after generation: %s", configPath)
	}

	// Write through a temporary file so an interrupted first run cannot leave a
	// half-rewritten config behind.
	info, err := os.Stat(configPath)
	if err != nil {
		return false, err
	}
	temporary := configPath + ".tmp"
	if err := os.WriteFile(temporary, updated, info.Mode().Perm()); err != nil {
		return false, fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(temporary, configPath); err != nil {
		return false, fmt.Errorf("replace config: %w", err)
	}
	return true, nil
}

// Launcher owns the bundled PostgreSQL lifecycle for one process.
type Launcher struct {
	layout   Layout
	out      io.Writer
	logger   *slog.Logger
	pgHost   string
	pgPort   int
	pgUser   string
	dbName   string
	httpAddr string

	startedPostgres bool
}

func New(layout Layout, out io.Writer, logger *slog.Logger) *Launcher {
	return &Launcher{
		layout: layout,
		out:    out,
		logger: logger,
		pgHost: defaultPGHost,
		pgPort: defaultPGPort,
		pgUser: defaultPGUser,
		dbName: defaultDBName,
	}
}

// SetDatabase derives the cluster settings from the configured PostgreSQL URL so
// the launcher starts the bundled server exactly where the app expects it.
func (l *Launcher) SetDatabase(rawURL string) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return
	}
	if host := parsed.Hostname(); host != "" {
		l.pgHost = host
	}
	if port := parsed.Port(); port != "" {
		if value, err := strconv.Atoi(port); err == nil {
			l.pgPort = value
		}
	}
	if user := parsed.User.Username(); user != "" {
		l.pgUser = user
	}
	if name := strings.TrimPrefix(parsed.Path, "/"); name != "" {
		l.dbName = name
	}
}

// SetHTTPAddress records where the server will listen, for the browser and the
// already-running check.
func (l *Launcher) SetHTTPAddress(address string) { l.httpAddr = address }

// ServerURL is the address a user should open, normalising a wildcard bind.
func (l *Launcher) ServerURL() string {
	address := strings.TrimSpace(l.httpAddr)
	if address == "" {
		address = "127.0.0.1:8080"
	}
	if host, port, err := net.SplitHostPort(address); err == nil {
		if host == "" || host == "0.0.0.0" || host == "::" {
			address = net.JoinHostPort("127.0.0.1", port)
		}
	}
	return "http://" + address
}

// AlreadyRunning reports whether something already answers on the HTTP address,
// in which case the caller should just open the browser.
func (l *Launcher) AlreadyRunning(ctx context.Context) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, l.ServerURL()+"/health/live", nil)
	if err != nil {
		return false
	}
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode == http.StatusOK
}

// Prepare brings the bundled database up: first-run initialisation, then start.
func (l *Launcher) Prepare(ctx context.Context) error {
	if err := os.MkdirAll(l.layout.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	if err := l.ensureCluster(ctx); err != nil {
		return err
	}
	return l.startPostgres(ctx)
}

// ensureCluster initialises the cluster and creates the database on first run.
func (l *Launcher) ensureCluster(ctx context.Context) error {
	if isFile(filepath.Join(l.layout.PGData, "PG_VERSION")) {
		return nil
	}
	fmt.Fprintln(l.out, "[1/3] 首次运行：初始化内置 PostgreSQL（约需 1 分钟，请勿关闭窗口）...")
	if err := os.MkdirAll(filepath.Dir(l.layout.PGData), 0o755); err != nil {
		return err
	}
	initdb := l.layout.binary("initdb")
	if err := l.runToLog(ctx, initdb, []string{
		"-D", l.layout.PGData,
		"-U", l.pgUser,
		"-A", "trust",
		"--encoding=UTF8",
		"--locale=C",
	}, initdbLogName); err != nil {
		return fmt.Errorf("initialize bundled PostgreSQL (see %s): %w", filepath.Join(l.layout.DataDir, initdbLogName), err)
	}

	// A single-user backend is the only way to create a database before the
	// server accepts connections.
	fmt.Fprintf(l.out, "      创建 %s 数据库...\n", l.dbName)
	single := exec.CommandContext(ctx, l.layout.postgresBinary(), "--single", "-D", l.layout.PGData, l.pgUser)
	single.Stdin = strings.NewReader("CREATE DATABASE " + quoteIdentifier(l.dbName) + ";\n")
	if err := l.appendToLog(single, initdbLogName); err != nil {
		return fmt.Errorf("create %s database (see %s): %w", l.dbName, filepath.Join(l.layout.DataDir, initdbLogName), err)
	}
	return nil
}

func (l *Launcher) startPostgres(ctx context.Context) error {
	pgCtl := l.layout.binary("pg_ctl")
	if l.postgresRunning(ctx, pgCtl) {
		l.logger.Info("bundled.postgres.already_running", "data", l.layout.PGData)
		return nil
	}
	fmt.Fprintf(l.out, "[2/3] 启动内置 PostgreSQL（%s:%d）...\n", l.pgHost, l.pgPort)
	options := fmt.Sprintf("-p %d -c listen_addresses=%s", l.pgPort, l.pgHost)
	// pg_ctl's own progress goes to the console, NOT to postgres.log: pg_ctl
	// already redirects the server's output to that path with -l, and two writers
	// on the same file make the server fail to start on Windows.
	command := exec.CommandContext(ctx, pgCtl,
		"-D", l.layout.PGData,
		"-l", filepath.Join(l.layout.DataDir, postgresLogName),
		"-o", options,
		"-w", "start",
	)
	command.Stdout = l.out
	command.Stderr = l.out
	if err := command.Run(); err != nil {
		return fmt.Errorf("start bundled PostgreSQL (see %s): %w", filepath.Join(l.layout.DataDir, postgresLogName), err)
	}
	l.startedPostgres = true
	return nil
}

func (l *Launcher) postgresRunning(ctx context.Context, pgCtl string) bool {
	probe, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return exec.CommandContext(probe, pgCtl, "status", "-D", l.layout.PGData).Run() == nil
}

// StopPostgres stops only the server this process started, so an externally
// managed PostgreSQL is left alone. It deliberately ignores the caller's context:
// it runs during shutdown, when that context is already cancelled.
func (l *Launcher) StopPostgres() {
	if !l.startedPostgres {
		return
	}
	fmt.Fprintln(l.out, "正在停止内置 PostgreSQL ...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, l.layout.binary("pg_ctl"),
		"-D", l.layout.PGData, "-m", "fast", "-w", "stop",
	).Run(); err != nil {
		l.logger.Warn("bundled.postgres.stop_failed", "error", err)
		return
	}
	l.startedPostgres = false
}

// OpenBrowserWhenReady waits for the server to answer and then opens the default
// browser. Failures are logged, never fatal: the URL is printed either way.
func (l *Launcher) OpenBrowserWhenReady(ctx context.Context, url string) {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		if probeErr := probe(ctx, url+"/health/live"); probeErr == nil {
			if err := OpenBrowser(url); err != nil {
				l.logger.Warn("bundled.browser.open_failed", "error", err, "url", url)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
	l.logger.Warn("bundled.browser.timeout", "url", url)
}

func probe(ctx context.Context, url string) error {
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", response.StatusCode)
	}
	return nil
}

func (l *Launcher) runToLog(ctx context.Context, name string, args []string, logName string) error {
	return l.appendToLog(exec.CommandContext(ctx, name, args...), logName)
}

func (l *Launcher) appendToLog(command *exec.Cmd, logName string) error {
	logFile, err := os.OpenFile(filepath.Join(l.layout.DataDir, logName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	command.Stdout = logFile
	command.Stderr = logFile
	// Stdin is deliberately left alone: the single-user backend run needs the
	// caller's SQL script piped into it.
	return command.Run()
}

// quoteIdentifier makes a database name safe to interpolate into SQL.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// OpenBrowser hands url to the platform's default browser without waiting for it.
func OpenBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		command = exec.Command("open", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
