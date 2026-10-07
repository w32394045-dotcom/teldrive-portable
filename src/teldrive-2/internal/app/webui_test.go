package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestWebUIHandlerServesAssetsAndHistoryFallback(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<main>Teldrive UI</main>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "app-deadbeef.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := webUIHandler{files: os.DirFS(root)}

	for _, test := range []struct {
		path        string
		status      int
		body        string
		cachePrefix string
	}{
		{path: "/", status: http.StatusOK, body: "Teldrive UI", cachePrefix: "no-cache"},
		{path: "/files/deep/folder", status: http.StatusOK, body: "Teldrive UI", cachePrefix: "no-cache"},
		{path: "/assets/app-deadbeef.js", status: http.StatusOK, body: "console.log", cachePrefix: "public, max-age=31536000"},
		{path: "/assets/missing.js", status: http.StatusNotFound, body: "404 page not found"},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			result := response.Result()
			defer result.Body.Close()
			content, _ := io.ReadAll(result.Body)
			if result.StatusCode != test.status || !strings.Contains(string(content), test.body) {
				t.Fatalf("GET %s = %d %q", test.path, result.StatusCode, content)
			}
			if test.cachePrefix != "" && !strings.HasPrefix(result.Header.Get("Cache-Control"), test.cachePrefix) {
				t.Fatalf("GET %s cache control = %q", test.path, result.Header.Get("Cache-Control"))
			}
			if test.status == http.StatusOK && !strings.Contains(result.Header.Get("Content-Security-Policy"), "script-src 'self'") {
				t.Fatalf("GET %s CSP = %q", test.path, result.Header.Get("Content-Security-Policy"))
			}
		})
	}
}

func TestWebUIHandlerServesPrecompressedSiblings(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Standing in for real brotli output: the handler must forward the bytes
	// verbatim and label them with the encoding of the sibling it picked.
	if err := os.WriteFile(filepath.Join(root, "assets", "app-deadbeef.js"), []byte("console.log('plain')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "app-deadbeef.js.br"), []byte("brotli-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "app-deadbeef.js.gz"), []byte("gzip-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<main>Teldrive UI</main>"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := webUIHandler{files: os.DirFS(root)}

	request := func(path, acceptEncoding string, method string, extra map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if acceptEncoding != "" {
			req.Header.Set("Accept-Encoding", acceptEncoding)
		}
		for key, value := range extra {
			req.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	t.Run("brotli wins when both are acceptable", func(t *testing.T) {
		response := request("/assets/app-deadbeef.js", "gzip, deflate, br", http.MethodGet, nil)
		if got := response.Body.String(); got != "brotli-bytes" {
			t.Fatalf("body = %q", got)
		}
		if got := response.Header().Get("Content-Encoding"); got != "br" {
			t.Fatalf("content-encoding = %q", got)
		}
		// Content type must describe the requested asset, not the sibling.
		if got := response.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
			t.Fatalf("content-type = %q", got)
		}
		if got := response.Header().Get("Cache-Control"); !strings.HasPrefix(got, "public, max-age=31536000") {
			t.Fatalf("cache-control = %q", got)
		}
		if got := response.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
			t.Fatalf("vary = %q", got)
		}
		if got := response.Header().Get("Content-Length"); got != strconv.Itoa(len("brotli-bytes")) {
			t.Fatalf("content-length = %q", got)
		}
	})

	t.Run("gzip fallback", func(t *testing.T) {
		response := request("/assets/app-deadbeef.js", "gzip, deflate", http.MethodGet, nil)
		if got := response.Body.String(); got != "gzip-bytes" {
			t.Fatalf("body = %q", got)
		}
		if got := response.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("content-encoding = %q", got)
		}
	})

	t.Run("q=0 refuses the encoding", func(t *testing.T) {
		response := request("/assets/app-deadbeef.js", "br;q=0, gzip", http.MethodGet, nil)
		if got := response.Body.String(); got != "gzip-bytes" {
			t.Fatalf("body = %q", got)
		}
	})

	t.Run("no accepted encoding serves identity bytes", func(t *testing.T) {
		response := request("/assets/app-deadbeef.js", "identity", http.MethodGet, nil)
		if got := response.Body.String(); got != "console.log('plain')" {
			t.Fatalf("body = %q", got)
		}
		if got := response.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("content-encoding = %q", got)
		}
	})

	t.Run("missing sibling falls back to identity", func(t *testing.T) {
		response := request("/index.html", "br", http.MethodGet, nil)
		if got := response.Body.String(); got != "<main>Teldrive UI</main>" {
			t.Fatalf("body = %q", got)
		}
		if got := response.Header().Get("Content-Encoding"); got != "" {
			t.Fatalf("content-encoding = %q", got)
		}
	})

	t.Run("conditional request revalidates per encoding", func(t *testing.T) {
		first := request("/assets/app-deadbeef.js", "br", http.MethodGet, nil)
		etag := first.Header().Get("ETag")
		if etag == "" {
			t.Fatal("no ETag on compressed response")
		}
		identity := request("/assets/app-deadbeef.js", "identity", http.MethodGet, nil)
		if identity.Header().Get("ETag") == etag {
			t.Fatal("compressed and identity responses must not share an ETag")
		}
		revalidated := request("/assets/app-deadbeef.js", "br", http.MethodGet, map[string]string{"If-None-Match": etag})
		if revalidated.Code != http.StatusNotModified {
			t.Fatalf("status = %d", revalidated.Code)
		}
		if revalidated.Body.Len() != 0 {
			t.Fatalf("304 carried a body: %q", revalidated.Body.String())
		}
	})

	t.Run("head reports length without a body", func(t *testing.T) {
		response := request("/assets/app-deadbeef.js", "br", http.MethodHead, nil)
		if response.Body.Len() != 0 {
			t.Fatalf("HEAD carried a body: %q", response.Body.String())
		}
		if got := response.Header().Get("Content-Length"); got != strconv.Itoa(len("brotli-bytes")) {
			t.Fatalf("content-length = %q", got)
		}
	})
}

func TestAcceptsEncoding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		header string
		name   string
		want   bool
	}{
		{header: "", name: "br", want: false},
		{header: "br", name: "br", want: true},
		{header: "gzip, deflate, br", name: "br", want: true},
		{header: "gzip, deflate, br", name: "zstd", want: false},
		{header: "br;q=0", name: "br", want: false},
		{header: "br;q=0.5, gzip;q=0.9", name: "br", want: true},
		{header: "*", name: "br", want: true},
		{header: "*;q=0", name: "br", want: false},
		{header: "br;q=0, *", name: "br", want: false},
		{header: "GZIP", name: "gzip", want: true},
	}
	for _, test := range tests {
		if got := acceptsEncoding(test.header, test.name); got != test.want {
			t.Errorf("acceptsEncoding(%q, %q) = %v, want %v", test.header, test.name, got, test.want)
		}
	}
}

func TestWebUIHandlerUsesEmbeddedSPA(t *testing.T) {
	t.Parallel()
	if handler, err := newWebUIHandler(); err != nil || handler == nil {
		t.Fatalf("embedded UI = %T, %v", handler, err)
	}
}

func TestRouteApplicationKeepsAPIAndSPASeparate(t *testing.T) {
	t.Parallel()
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "api:"+r.URL.Path)
	})
	ui := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ui:"+r.URL.Path)
	})
	mux := chi.NewRouter()
	routeApplication(mux, api, ui)

	tests := map[string]string{
		"/api/v1/files": "api:/v1/files",
		"/v1/files":     "api:/v1/files",
		"/health/live":  "api:/health/live",
		"/files":        "ui:/files",
	}
	for requestPath, want := range tests {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestPath, nil))
		if got := strings.TrimSpace(response.Body.String()); got != want {
			t.Fatalf("GET %s = %q, want %q", requestPath, got, want)
		}
	}
}
