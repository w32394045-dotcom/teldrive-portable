package bundled

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectRequiresTheBundledLayout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if _, ok := Detect(root); ok {
		t.Fatal("an empty directory must not be detected as a bundle")
	}

	writeFile(t, filepath.Join(root, "config.toml"), "x")
	if _, ok := Detect(root); ok {
		t.Fatal("a config without pgsql/bin/postgres must not be detected")
	}

	writeFile(t, filepath.Join(root, "pgsql", "bin", binaryName("postgres")), "")
	layout, ok := Detect(root)
	if !ok {
		t.Fatal("a complete layout should be detected")
	}
	if layout.ConfigPath != filepath.Join(root, "config.toml") {
		t.Fatalf("config path = %q", layout.ConfigPath)
	}
	if layout.PGData != filepath.Join(root, "data", "pgdata") {
		t.Fatalf("pgdata path = %q", layout.PGData)
	}
}

func TestEnsureKeysReplacesPlaceholdersOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	writeFile(t, configPath, "signing-key = \""+SigningKeyPlaceholder+"\"\ndata-key = \""+DataKeyPlaceholder+"\"\n")

	generated, err := EnsureKeys(configPath)
	if err != nil {
		t.Fatalf("EnsureKeys() error = %v", err)
	}
	if !generated {
		t.Fatal("EnsureKeys() reported nothing generated")
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Contains(text, SigningKeyPlaceholder) || strings.Contains(text, DataKeyPlaceholder) {
		t.Fatalf("placeholders survived: %s", text)
	}
	if !strings.Contains(text, "data-key = \"") || !strings.Contains(text, "signing-key = \"") {
		t.Fatalf("unexpected config content: %s", text)
	}

	// The signing key is 36 bytes and the data key 32, both base64 encoded.
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		value := line[strings.Index(line, `"`)+1 : strings.LastIndex(line, `"`)]
		decoded, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatalf("key %q is not base64: %v", value, err)
		}
		want := signingKeyBytes
		if strings.HasPrefix(line, "data-key") {
			want = dataKeyBytes
		}
		if len(decoded) != want {
			t.Fatalf("%s decoded to %d bytes, want %d", line, len(decoded), want)
		}
	}

	// Second run must be a no-op, so keys never rotate on restart.
	again, err := EnsureKeys(configPath)
	if err != nil {
		t.Fatalf("second EnsureKeys() error = %v", err)
	}
	if again {
		t.Fatal("EnsureKeys() rewrote a config that had no placeholders")
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != text {
		t.Fatal("config changed on the second run")
	}
}

func TestEnsureKeysHandlesOnePlaceholder(t *testing.T) {
	t.Parallel()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, configPath, "signing-key = \"already-set\"\ndata-key = \""+DataKeyPlaceholder+"\"\n")

	if _, err := EnsureKeys(configPath); err != nil {
		t.Fatalf("EnsureKeys() error = %v", err)
	}
	content, _ := os.ReadFile(configPath)
	if !strings.Contains(string(content), "already-set") {
		t.Fatalf("existing key was overwritten: %s", content)
	}
	if strings.Contains(string(content), DataKeyPlaceholder) {
		t.Fatalf("placeholder survived: %s", content)
	}
}

func TestSetDatabaseDerivesClusterSettings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		url        string
		wantHost   string
		wantPort   int
		wantUser   string
		wantDBName string
	}{
		{
			name: "bundled default", url: "postgres://postgres@127.0.0.1:5433/teldrive?sslmode=disable",
			wantHost: "127.0.0.1", wantPort: 5433, wantUser: "postgres", wantDBName: "teldrive",
		},
		{
			name: "custom port and name", url: "postgres://admin@localhost:6000/drive",
			wantHost: "localhost", wantPort: 6000, wantUser: "admin", wantDBName: "drive",
		},
		{
			name: "unparseable keeps defaults", url: "not a url",
			wantHost: defaultPGHost, wantPort: defaultPGPort, wantUser: defaultPGUser, wantDBName: defaultDBName,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			launcher := New(Layout{}, nil, nil)
			launcher.SetDatabase(test.url)
			if launcher.pgHost != test.wantHost || launcher.pgPort != test.wantPort {
				t.Fatalf("host:port = %s:%d, want %s:%d", launcher.pgHost, launcher.pgPort, test.wantHost, test.wantPort)
			}
			if launcher.pgUser != test.wantUser || launcher.dbName != test.wantDBName {
				t.Fatalf("user/db = %s/%s, want %s/%s", launcher.pgUser, launcher.dbName, test.wantUser, test.wantDBName)
			}
		})
	}
}

func TestServerURLNormalisesWildcardBind(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ address, want string }{
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"0.0.0.0:9000", "http://127.0.0.1:9000"},
		{"", "http://127.0.0.1:8080"},
	} {
		launcher := New(Layout{}, nil, nil)
		launcher.SetHTTPAddress(test.address)
		if got := launcher.ServerURL(); got != test.want {
			t.Errorf("ServerURL(%q) = %q, want %q", test.address, got, test.want)
		}
	}
}

func TestQuoteIdentifier(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ in, want string }{
		{"teldrive", `"teldrive"`},
		{`we"ird`, `"we""ird"`},
	} {
		if got := quoteIdentifier(test.in); got != test.want {
			t.Errorf("quoteIdentifier(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}
