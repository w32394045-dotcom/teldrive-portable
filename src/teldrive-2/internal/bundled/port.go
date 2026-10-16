package bundled

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The portable bundle cannot assume it owns a port. 8080 is the first thing
// every other development tool on a machine reaches for, so a drive that insists
// on it loses the race often enough to look broken -- and the failure mode is a
// server that will not start with no obvious reason why.
//
// So the bundled launcher owns the choice: it prefers the configured port, steps
// to the next free one when something else holds it, and records what it picked
// so the next start finds the running server instead of starting a rival.
const (
	// DefaultPort is used when the configuration names no port. It is
	// deliberately not 8080 or any other well-known alternative, and not in the
	// ephemeral range either, so the operating system will never hand it to an
	// outgoing connection while the server is running.
	DefaultPort = 18787
	// portProbeAttempts bounds the search. A machine with thirty-three
	// consecutive busy ports is not something to keep guessing about.
	portProbeAttempts = 32
	// portFileName records the port actually bound, in the data directory beside
	// the database. The configuration can only say what was *asked* for.
	portFileName = "port"
)

// SelectAddress picks the address the server should bind and records it.
//
// The configured host is honoured -- a wildcard bind stays a wildcard bind,
// because that is what the local-network feature needs -- while the port is the
// launcher's to choose.
func (l *Launcher) SelectAddress(ctx context.Context) (string, error) {
	host, port := splitListenAddress(l.httpAddr)
	for offset := 0; offset <= portProbeAttempts; offset++ {
		candidate := port + offset
		if candidate > 65535 {
			break
		}
		if !portIsFree(host, candidate) {
			continue
		}
		address := net.JoinHostPort(host, strconv.Itoa(candidate))
		l.SetHTTPAddress(address)
		if candidate != port {
			fmt.Fprintf(l.out, "      端口 %d 已被占用，改用 %d\n", port, candidate)
		}
		if err := l.recordPort(address); err != nil {
			// Not fatal: the file is an optimisation for the next start, and a
			// lost one only costs a probe.
			l.logger.Warn("bundle.port.not_recorded", "address", address, "error", err)
		}
		return address, nil
	}
	return "", fmt.Errorf("no free port between %d and %d", port, port+portProbeAttempts)
}

// RunningAddress returns the address of an instance that is already answering,
// or an empty string when there is none.
//
// The recorded port is checked before the configured one. After the port has
// moved once the configuration still names the old number, so a second
// double-click would otherwise miss the running server and start a second one on
// the next free port -- two databases and two servers on one machine.
func (l *Launcher) RunningAddress(ctx context.Context) string {
	if recorded := l.recordedAddress(); recorded != "" {
		if healthy(ctx, "http://"+recorded) {
			l.SetHTTPAddress(recorded)
			return "http://" + recorded
		}
		// Stale: nothing is there any more, so the file is not evidence of
		// anything and the port is free to be reused.
		l.forgetPort()
	}
	url := l.ServerURL()
	if healthy(ctx, url) {
		return url
	}
	return ""
}

// RecordedAddress reports where the last run listened, for callers that need the
// address rather than a liveness answer.
func (l *Launcher) RecordedAddress() string {
	address := l.recordedAddress()
	if address == "" {
		return ""
	}
	return "http://" + address
}

// ServerURL is the address a user should open, normalising a wildcard bind.
func (l *Launcher) ServerURL() string {
	address := strings.TrimSpace(l.httpAddr)
	if address == "" {
		address = net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultPort))
	}
	if host, port, err := net.SplitHostPort(address); err == nil {
		if host == "" || host == "0.0.0.0" || host == "::" {
			address = net.JoinHostPort("127.0.0.1", port)
		}
	}
	return "http://" + address
}

// AlreadyRunning reports whether an instance is already answering, which is the
// case where the caller should just open the browser.
func (l *Launcher) AlreadyRunning(ctx context.Context) bool {
	return l.RunningAddress(ctx) != ""
}

// portPath is the file that remembers the chosen port.
func (l *Launcher) portPath() string {
	return filepath.Join(l.layout.DataDir, portFileName)
}

func (l *Launcher) recordedAddress() string {
	if l == nil {
		return ""
	}
	raw, err := os.ReadFile(l.portPath())
	if err != nil {
		return ""
	}
	address := strings.TrimSpace(string(raw))
	// Only a bare host:port is accepted. The file is small and hand-editable,
	// and a value that is not an address must not become a request target.
	if _, _, err := net.SplitHostPort(address); err != nil {
		return ""
	}
	return address
}

func (l *Launcher) recordPort(address string) error {
	directory := l.layout.DataDir
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	return os.WriteFile(l.portPath(), []byte(address+"\n"), 0o644)
}

func (l *Launcher) forgetPort() {
	if err := os.Remove(l.portPath()); err != nil && !os.IsNotExist(err) {
		l.logger.Warn("bundle.port.not_cleared", "error", err)
	}
}

// splitListenAddress returns the host to bind and the port to start from,
// falling back to the loopback default when the configuration is unusable.
func splitListenAddress(address string) (string, int) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "127.0.0.1", DefaultPort
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "127.0.0.1", DefaultPort
	}
	value, err := strconv.Atoi(port)
	if err != nil || value <= 0 || value > 65535 {
		return "127.0.0.1", DefaultPort
	}
	if host == "" {
		// "":8080 means every interface; keep that intent rather than quietly
		// narrowing it to the loopback.
		host = ""
	}
	return host, value
}

// portIsFree reports whether the address can be bound right now.
//
// Listening is the only honest test: it asks the operating system the same
// question the server is about to ask, and it also catches the case of another
// process holding the port without accepting connections.
func portIsFree(host string, port int) bool {
	listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// healthy reports whether a Teldrive instance answers at the URL.
func healthy(ctx context.Context, url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url+"/health/live", nil)
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
