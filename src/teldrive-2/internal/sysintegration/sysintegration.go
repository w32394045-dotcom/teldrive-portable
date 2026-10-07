// Package sysintegration talks to the operating system on the user's behalf:
// starting Teldrive at login, and mounting its WebDAV tree as a system drive.
//
// Everything that touches the OS goes through the small interfaces below, so the
// decision logic (which drive letter is free, what `net use` said, whether the
// Windows WebDAV redirector is configured) is unit-testable without a real
// registry or a real mount.
package sysintegration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Registry reads and writes the handful of values this package needs.
type Registry interface {
	// ReadDWORD returns a numeric value; ok is false when it is not set.
	ReadDWORD(hive, path, name string) (value uint32, ok bool)
	// ReadString returns a string value; ok is false when it is not set.
	ReadString(hive, path, name string) (string, bool)
	WriteString(hive, path, name, value string) error
	DeleteValue(hive, path, name string) error
}

// Runner executes an external command.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

const (
	// HiveCurrentUser and HiveLocalMachine name the two registry roots used here.
	HiveCurrentUser  = "HKCU"
	HiveLocalMachine = "HKLM"

	// autostartValueName is the Run entry Teldrive owns.
	autostartValueName = "Teldrive"
	runKeyPath         = `Software\Microsoft\Windows\CurrentVersion\Run`

	// webClientKeyPath holds the Windows WebDAV redirector settings, all of which
	// need elevation to change.
	webClientKeyPath     = `SYSTEM\CurrentControlSet\Services\WebClient\Parameters`
	webClientServicePath = `SYSTEM\CurrentControlSet\Services\WebClient`
	basicAuthLevelName   = "BasicAuthLevel"
	fileSizeLimitName    = "FileSizeLimitInBytes"
	serviceStartName     = "Start"
	// basicAuthLevelHTTP is the value that permits Basic auth over plain HTTP;
	// the default (1) allows it over TLS only, which a loopback server cannot use.
	basicAuthLevelHTTP = 2
	// recommendedFileSizeLimit lifts Windows' 50 MB WebDAV download cap.
	recommendedFileSizeLimit = 4294967295
	// minFileSizeLimit is the point below which a cloud drive is unusable.
	minFileSizeLimit = 1024 * 1024 * 1024

	// autostartFlag is passed to the executable so it can start quietly at login.
	autostartFlag = "--autostart"

	// driveLetters is searched left to right for a free letter.
	driveLetters = "ZYXWVUTSRQPONMLKJIHGFED"
)

// ErrUnsupported reports an operation this platform cannot perform.
var ErrUnsupported = errors.New("not supported on this platform")

// Manager performs the OS integration.
type Manager struct {
	exePath  string
	registry Registry
	runner   Runner
	platform string
}

func New(exePath string) *Manager {
	return &Manager{
		exePath:  exePath,
		registry: platformRegistry{},
		runner:   execRunner{},
		platform: platformName(),
	}
}

// newWithDeps builds a Manager with injected collaborators, for tests.
func newWithDeps(exePath string, registry Registry, runner Runner, platform string) *Manager {
	return &Manager{exePath: exePath, registry: registry, runner: runner, platform: platform}
}

// AutostartStatus describes whether Teldrive starts at login.
type AutostartStatus struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Command   string `json:"command,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// AutostartCommand is the command line stored in the Run key. It quotes the path
// (it usually contains spaces) and marks the launch so the app stays quiet.
//
// The quotes are added by hand on purpose: Go's %q escapes every backslash, which
// is right for a Go literal but wrong for a Windows command line.
func (m *Manager) AutostartCommand() string {
	return `"` + m.exePath + `" ` + autostartFlag
}

func (m *Manager) Autostart(ctx context.Context) AutostartStatus {
	if m.platform != "windows" {
		return AutostartStatus{Detail: "只支持 Windows。"}
	}
	value, ok := m.registry.ReadString(HiveCurrentUser, runKeyPath, autostartValueName)
	if !ok {
		return AutostartStatus{Supported: true, Enabled: false, Command: m.AutostartCommand()}
	}
	return AutostartStatus{
		Supported: true,
		Enabled:   true,
		Command:   value,
		Detail:    value,
	}
}

func (m *Manager) SetAutostart(ctx context.Context, enabled bool) error {
	if m.platform != "windows" {
		return ErrUnsupported
	}
	if !enabled {
		return m.registry.DeleteValue(HiveCurrentUser, runKeyPath, autostartValueName)
	}
	return m.registry.WriteString(HiveCurrentUser, runKeyPath, autostartValueName, m.AutostartCommand())
}

// Elevate runs FixCommand with administrator rights. Windows raises a UAC
// prompt, which the user has to accept — there is no way around that for HKLM.
//
// The script is written to a file and executed with -File rather than passed
// with -Command: the command contains quotes and backslashes that would need
// several layers of escaping otherwise.
func (m *Manager) Elevate(ctx context.Context) error {
	if m.platform != "windows" {
		return ErrUnsupported
	}
	path, err := m.writeFixScript()
	if err != nil {
		return err
	}
	launch := fmt.Sprintf(
		"Start-Process -FilePath powershell -Verb RunAs -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-WindowStyle','Hidden','-File','%s'",
		path,
	)
	output, err := m.runner.Run(ctx, "powershell", "-NoProfile", "-Command", launch)
	if err != nil {
		// A refused UAC prompt surfaces here; say so plainly instead of leaking
		// a bare exit status.
		return fmt.Errorf("提权被取消或失败（请在 UAC 对话框中点“是”）：%s", firstLine(string(output)))
	}
	return nil
}

func (m *Manager) writeFixScript() (string, error) {
	path := filepath.Join(os.TempDir(), "teldrive-webdav-fix.ps1")
	if err := os.WriteFile(path, []byte(m.FixCommand()+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("写入修复脚本失败: %w", err)
	}
	return path, nil
}

// Platform reports the OS this Manager targets, for the API response.
func (m *Manager) Platform() string { return m.platform }

// PrerequisiteItem is one check the Windows WebDAV redirector has to pass.
type PrerequisiteItem struct {
	Key         string `json:"key"`
	OK          bool   `json:"ok"`
	Current     string `json:"current,omitempty"`
	Required    string `json:"required,omitempty"`
	Description string `json:"description"`
}

// Prerequisites summarises the redirector configuration. Changing any of it
// needs administrator rights, so the UI has to be able to explain that.
type Prerequisites struct {
	Ready      bool               `json:"ready"`
	Items      []PrerequisiteItem `json:"items"`
	FixCommand string             `json:"fixCommand"`
	NeedsAdmin bool               `json:"needsAdmin"`
}

// CheckPrerequisites reads the redirector settings and reports what is missing.
func (m *Manager) CheckPrerequisites(ctx context.Context) Prerequisites {
	if m.platform != "windows" {
		return Prerequisites{Items: []PrerequisiteItem{{
			Key: "platform", OK: false, Description: "只支持 Windows。",
		}}}
	}

	var items []PrerequisiteItem

	serviceStart, startOK := m.registry.ReadDWORD(HiveLocalMachine, webClientServicePath, serviceStartName)
	// 2 = automatic, 3 = manual, 4 = disabled. Windows starts a manual service on
	// demand, so only "disabled" is fatal — but automatic is what makes a mapped
	// drive survive a reboot.
	serviceDescription := "WebClient（WebDAV 重定向器）服务需为自动启动，映射盘才能在重启后可用。"
	items = append(items, PrerequisiteItem{
		Key:         "webclient_service",
		OK:          startOK && serviceStart == 2,
		Current:     describeServiceStart(serviceStart, startOK),
		Required:    "自动",
		Description: serviceDescription,
	})

	authLevel, authOK := m.registry.ReadDWORD(HiveLocalMachine, webClientKeyPath, basicAuthLevelName)
	items = append(items, PrerequisiteItem{
		Key:         "basic_auth_level",
		OK:          authOK && authLevel >= basicAuthLevelHTTP,
		Current:     describeNumber(authLevel, authOK),
		Required:    strconv.Itoa(basicAuthLevelHTTP),
		Description: "Windows 默认只允许通过 HTTPS 使用 Basic 认证；本机服务是 http，需要把 BasicAuthLevel 设为 2。",
	})

	sizeLimit, sizeOK := m.registry.ReadDWORD(HiveLocalMachine, webClientKeyPath, fileSizeLimitName)
	items = append(items, PrerequisiteItem{
		Key:         "file_size_limit",
		OK:          sizeOK && sizeLimit >= minFileSizeLimit,
		Current:     describeBytes(sizeLimit, sizeOK),
		Required:    ">= 1 GB",
		Description: "Windows 默认限制 WebDAV 下载为 50 MB，超过就会报错，需要调高 FileSizeLimitInBytes。",
	})

	ready := true
	for _, item := range items {
		if !item.OK {
			ready = false
		}
	}
	return Prerequisites{
		Ready:      ready,
		Items:      items,
		NeedsAdmin: !ready,
		FixCommand: m.FixCommand(),
	}
}

// FixCommand is the elevated PowerShell that brings the redirector into shape. It
// is both run for the user and shown so they can run it themselves.
func (m *Manager) FixCommand() string {
	steps := []string{
		fmt.Sprintf("Set-ItemProperty -Path 'HKLM:\\%s' -Name '%s' -Value %d -Type DWord", webClientServicePath, serviceStartName, 2),
		fmt.Sprintf("Set-ItemProperty -Path 'HKLM:\\%s' -Name '%s' -Value %d -Type DWord", webClientKeyPath, basicAuthLevelName, basicAuthLevelHTTP),
		fmt.Sprintf("Set-ItemProperty -Path 'HKLM:\\%s' -Name '%s' -Value %d -Type DWord", webClientKeyPath, fileSizeLimitName, recommendedFileSizeLimit),
		"Set-Service -Name WebClient -StartupType Automatic",
		"Start-Service -Name WebClient",
	}
	return strings.Join(steps, "; ")
}

func describeServiceStart(value uint32, ok bool) string {
	if !ok {
		return "未设置"
	}
	switch value {
	case 2:
		return "自动"
	case 3:
		return "手动"
	case 4:
		return "已禁用"
	default:
		return strconv.FormatUint(uint64(value), 10)
	}
}

func describeNumber(value uint32, ok bool) string {
	if !ok {
		return "未设置"
	}
	return strconv.FormatUint(uint64(value), 10)
}

func describeBytes(value uint32, ok bool) string {
	if !ok {
		return "未设置"
	}
	return fmt.Sprintf("%.0f MB", float64(value)/1024/1024)
}

// MountStatus describes the mapped drive.
type MountStatus struct {
	Supported     bool          `json:"supported"`
	Mounted       bool          `json:"mounted"`
	Drive         string        `json:"drive,omitempty"`
	URL           string        `json:"url,omitempty"`
	Detail        string        `json:"detail,omitempty"`
	Prerequisites Prerequisites `json:"prerequisites"`
}

// MountStatus reports whether the DAV tree is currently mapped, where, and
// whether the redirector is configured for it.
func (m *Manager) MountStatus(ctx context.Context, url string) MountStatus {
	status := MountStatus{URL: url, Prerequisites: m.CheckPrerequisites(ctx)}
	if m.platform != "windows" {
		status.Detail = "只支持 Windows。"
		return status
	}
	status.Supported = true

	output, err := m.runner.Run(ctx, "net", "use")
	if err != nil && len(output) == 0 {
		status.Detail = strings.TrimSpace(err.Error())
		return status
	}
	if drive, ok := findMountForURL(string(output), url); ok {
		status.Mounted = true
		status.Drive = drive
		return status
	}
	status.Drive = m.pickDriveLetter(string(output))
	return status
}

// Mount maps the DAV tree to a drive letter and remembers it across reboots.
func (m *Manager) Mount(ctx context.Context, url, user, password, drive string) (string, error) {
	if m.platform != "windows" {
		return "", ErrUnsupported
	}
	if drive == "" {
		output, _ := m.runner.Run(ctx, "net", "use")
		drive = m.pickDriveLetter(string(output))
	}
	if drive == "" {
		return "", errors.New("没有可用的盘符")
	}
	// /persistent:yes keeps the mapping after a reboot, which is the point of
	// pairing this with autostart.
	args := []string{"use", drive, url, fmt.Sprintf("/user:%s", user), password, "/persistent:yes"}
	if output, err := m.runner.Run(ctx, "net", args...); err != nil {
		return drive, fmt.Errorf("%s: %s", err, firstLine(string(output)))
	}
	return drive, nil
}

// Unmount removes the mapping.
func (m *Manager) Unmount(ctx context.Context, drive string) error {
	if m.platform != "windows" {
		return ErrUnsupported
	}
	drive = strings.TrimSpace(drive)
	if drive == "" {
		return errors.New("未指定盘符")
	}
	if output, err := m.runner.Run(ctx, "net", "use", drive, "/delete", "/y"); err != nil {
		return fmt.Errorf("%s: %s", err, firstLine(string(output)))
	}
	return nil
}

// mountRow is one parsed row of `net use` output.
type mountRow struct {
	drive  string // "Z:"
	target string // normalized remote path, empty when the row has none
}

// parseNetUse extracts the mappings from `net use` output. The letter is not the
// first field: a row looks like
//
//	OK           Z:        \\127.0.0.1@8080\webdav     Web Client Network
//
// so every field is examined, which also copes with localized headers.
func parseNetUse(output string) []mountRow {
	var rows []mountRow
	for _, line := range strings.Split(output, "\n") {
		var row mountRow
		for _, field := range strings.Fields(line) {
			switch {
			case row.drive == "" && isDriveLetter(field):
				row.drive = strings.ToUpper(field)
			case row.target == "" && looksLikeRemote(field):
				row.target = normalizeMountURL(field)
			}
		}
		if row.drive != "" {
			rows = append(rows, row)
		}
	}
	return rows
}

func isDriveLetter(value string) bool {
	if len(value) != 2 || value[1] != ':' {
		return false
	}
	letter := value[0]
	return (letter >= 'A' && letter <= 'Z') || (letter >= 'a' && letter <= 'z')
}

func looksLikeRemote(value string) bool {
	lowered := strings.ToLower(value)
	return strings.HasPrefix(value, `\\`) || strings.HasPrefix(lowered, "http://") || strings.HasPrefix(lowered, "https://")
}

// pickDriveLetter returns the first unused letter from driveLetters.
func (m *Manager) pickDriveLetter(netUseOutput string) string {
	used := map[string]bool{}
	for _, row := range parseNetUse(netUseOutput) {
		used[strings.TrimSuffix(row.drive, ":")] = true
	}
	for _, candidate := range driveLetters {
		letter := string(candidate)
		if !used[letter] {
			return letter + ":"
		}
	}
	return ""
}

// findMountForURL reports the drive letter already mapping url, if any. Windows
// reports a mapped WebDAV path as a UNC, so both spellings are compared.
func findMountForURL(netUseOutput, url string) (string, bool) {
	wanted := normalizeMountURL(url)
	for _, row := range parseNetUse(netUseOutput) {
		if row.target != "" && row.target == wanted {
			return row.drive, true
		}
	}
	return "", false
}

func normalizeMountURL(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.TrimSuffix(normalized, "/")
	// Windows reports a mapped WebDAV path as a UNC (\\host@port\path) even when
	// it was mounted from a URL, so compare on the meaningful parts only.
	normalized = strings.TrimPrefix(normalized, `\\`)
	normalized = strings.TrimPrefix(normalized, "http://")
	normalized = strings.TrimPrefix(normalized, "https://")
	normalized = strings.ReplaceAll(normalized, "@", ":")
	normalized = strings.ReplaceAll(normalized, `\`, "/")
	return normalized
}

func firstLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// sortedKeys is a small helper used by tests and diagnostics.
func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
