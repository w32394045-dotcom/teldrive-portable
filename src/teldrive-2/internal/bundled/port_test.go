package bundled

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func testLayout(t *testing.T) Layout {
	t.Helper()
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatalf("create data directory: %v", err)
	}
	return Layout{Root: root, DataDir: data}
}

// occupy binds a port for the duration of the test and returns it.
func occupy(t *testing.T, host string) int {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener.Addr().(*net.TCPAddr).Port
}

// TestSelectAddressStepsOverABusyPort is the whole reason this exists: the
// bundle cannot assume it owns the port it was configured with.
func TestSelectAddressStepsOverABusyPort(t *testing.T) {
	layout := testLayout(t)
	busy := occupy(t, "127.0.0.1")
	launcher := New(layout, &strings.Builder{}, nil)
	launcher.SetHTTPAddress(net.JoinHostPort("127.0.0.1", strconv.Itoa(busy)))

	address, err := launcher.SelectAddress(context.Background())
	if err != nil {
		t.Fatalf("SelectAddress: %v", err)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split chosen address %q: %v", address, err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("host = %q, want the configured one", host)
	}
	chosen, _ := strconv.Atoi(port)
	if chosen == busy {
		t.Fatalf("chose the port that was already busy (%d)", busy)
	}
	if chosen <= busy || chosen > busy+portProbeAttempts {
		t.Fatalf("chose %d, which is outside the search window above %d", chosen, busy)
	}
	// The choice has to be usable, not merely different.
	if !portIsFree(host, chosen) {
		t.Fatalf("chose %d, which is not actually free", chosen)
	}
}

// freePort asks the operating system for a port and gives it straight back, so
// the test has a number that is very likely still free.
func freePort(t *testing.T, host string) int {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatalf("find a free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release the port: %v", err)
	}
	return port
}

func TestSelectAddressKeepsThePortWhenItIsFree(t *testing.T) {
	layout := testLayout(t)
	free := freePort(t, "127.0.0.1")

	launcher := New(layout, &strings.Builder{}, nil)
	launcher.SetHTTPAddress(net.JoinHostPort("127.0.0.1", strconv.Itoa(free)))
	address, err := launcher.SelectAddress(context.Background())
	if err != nil {
		t.Fatalf("SelectAddress: %v", err)
	}
	if address != net.JoinHostPort("127.0.0.1", strconv.Itoa(free)) {
		t.Fatalf("address = %q, want the configured free port %d", address, free)
	}
}

// TestSelectAddressRecordsThePort is what makes a second double-click harmless:
// the configuration still names the requested port, so the running one has to be
// discoverable somewhere else.
func TestSelectAddressRecordsThePort(t *testing.T) {
	layout := testLayout(t)
	launcher := New(layout, &strings.Builder{}, nil)
	launcher.SetHTTPAddress("127.0.0.1:18787")

	address, err := launcher.SelectAddress(context.Background())
	if err != nil {
		t.Fatalf("SelectAddress: %v", err)
	}
	recorded, err := os.ReadFile(filepath.Join(layout.DataDir, portFileName))
	if err != nil {
		t.Fatalf("read the port file: %v", err)
	}
	if strings.TrimSpace(string(recorded)) != address {
		t.Fatalf("recorded %q, want %q", strings.TrimSpace(string(recorded)), address)
	}
}

// TestRunningAddressPrefersTheRecordedPort covers the case that would otherwise
// start a second server and a second database on one machine.
func TestRunningAddressPrefersTheRecordedPort(t *testing.T) {
	layout := testLayout(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health/live" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	running := strings.TrimPrefix(server.URL, "http://")
	if err := os.WriteFile(filepath.Join(layout.DataDir, portFileName), []byte(running+"\n"), 0o644); err != nil {
		t.Fatalf("write the port file: %v", err)
	}
	// The configuration names a different port, which is exactly the state after
	// the first run had to step over a busy one.
	launcher := New(layout, &strings.Builder{}, nil)
	launcher.SetHTTPAddress("127.0.0.1:18787")

	url := launcher.RunningAddress(context.Background())
	if url != server.URL {
		t.Fatalf("RunningAddress = %q, want the recorded %q", url, server.URL)
	}
	if !launcher.AlreadyRunning(context.Background()) {
		t.Fatal("AlreadyRunning should agree with RunningAddress")
	}
	// Having found it, the launcher now points at it, so anything built from
	// ServerURL follows the running instance rather than the stale configuration.
	if got := launcher.ServerURL(); got != server.URL {
		t.Fatalf("ServerURL = %q, want %q", got, server.URL)
	}
}

// TestRunningAddressDiscardsAStalePort makes sure a crashed run does not leave a
// file behind that makes the next start believe something is listening.
func TestRunningAddressDiscardsAStalePort(t *testing.T) {
	layout := testLayout(t)
	dead := occupy(t, "127.0.0.1")
	path := filepath.Join(layout.DataDir, portFileName)
	if err := os.WriteFile(path, []byte(net.JoinHostPort("127.0.0.1", strconv.Itoa(dead))+"\n"), 0o644); err != nil {
		t.Fatalf("write the port file: %v", err)
	}
	launcher := New(layout, &strings.Builder{}, nil)
	launcher.SetHTTPAddress("127.0.0.1:18787")

	if url := launcher.RunningAddress(context.Background()); url != "" {
		t.Fatalf("RunningAddress = %q, want nothing: the recorded port answers nothing", url)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the stale port file was kept: %v", err)
	}
}

func TestRecordedAddressRejectsGarbage(t *testing.T) {
	layout := testLayout(t)
	path := filepath.Join(layout.DataDir, portFileName)
	launcher := New(layout, &strings.Builder{}, nil)

	for name, content := range map[string]string{
		"empty":         "",
		"just a number": "18787",
		"a url":         "http://127.0.0.1:18787",
		"host only":     "127.0.0.1",
		"nonsense":      "not an address at all",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatalf("write the port file: %v", err)
			}
			if got := launcher.recordedAddress(); got != "" {
				t.Fatalf("recordedAddress(%q) = %q, want empty", content, got)
			}
		})
	}
}

func TestSplitListenAddressFallsBackToTheDefault(t *testing.T) {
	for name, test := range map[string]struct {
		address   string
		wantHost  string
		wantPort  int
		expectAny bool
	}{
		"empty":             {"", "127.0.0.1", DefaultPort, false},
		"current default":   {"127.0.0.1:8080", "127.0.0.1", 8080, false},
		"wildcard":          {"0.0.0.0:9000", "0.0.0.0", 9000, false},
		"any interface":     {":9001", "", 9001, false},
		"no port":           {"127.0.0.1", "127.0.0.1", DefaultPort, false},
		"port out of range": {"127.0.0.1:70000", "127.0.0.1", DefaultPort, false},
		"port zero":         {"127.0.0.1:0", "127.0.0.1", DefaultPort, false},
		"negative port":     {"127.0.0.1:-1", "127.0.0.1", DefaultPort, false},
	} {
		t.Run(name, func(t *testing.T) {
			host, port := splitListenAddress(test.address)
			if host != test.wantHost || port != test.wantPort {
				t.Fatalf("splitListenAddress(%q) = %q,%d want %q,%d",
					test.address, host, port, test.wantHost, test.wantPort)
			}
		})
	}
}

func TestDefaultPortIsNotAWellKnownOne(t *testing.T) {
	// The point of the constant is that it does not collide with the tools a
	// developer already has running.
	for _, common := range []int{80, 443, 3000, 5000, 8000, 8080, 8081, 8443, 8888, 9000, 9090, 10000} {
		if DefaultPort == common {
			t.Fatalf("DefaultPort is the commonly used %d", common)
		}
	}
	if DefaultPort < 1024 || DefaultPort > 49151 {
		t.Fatalf("DefaultPort = %d, want a registered port rather than a privileged or ephemeral one", DefaultPort)
	}
}
