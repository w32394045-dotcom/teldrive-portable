package app

import (
	"bytes"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	embeddedui "github.com/tgdrive/teldrive/v2/ui"
)

// precompressedEncodings lists the encodings the UI build may emit as
// `<asset>.<suffix>` siblings, most preferred first. `ui/vite.config.mts`
// writes brotli siblings; gzip is honoured too so an older or custom build
// keeps working without any code change.
var precompressedEncodings = []struct{ name, suffix string }{
	{"br", "br"},
	{"gzip", "gz"},
}

func newWebUIHandler() (http.Handler, error) {
	files, err := fs.Sub(embeddedui.StaticFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("open embedded UI: %w", err)
	}
	if _, err := fs.Stat(files, "index.html"); err != nil {
		return nil, fmt.Errorf("inspect embedded UI index: %w", err)
	}
	return webUIHandler{files: files}, nil
}

type webUIHandler struct {
	files fs.FS
}

func (h webUIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self' blob:; script-src 'self'; style-src 'self' 'unsafe-inline' blob:; img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data: blob:; connect-src 'self' data: blob:; worker-src 'self' blob:; frame-src blob: data:; object-src 'none'; base-uri 'self'; form-action 'self'")
	// Responses differ by request encoding, so any shared cache in front of the
	// server must key on it. Without this a compressed asset could be replayed
	// to a client that cannot decode it.
	w.Header().Add("Vary", "Accept-Encoding")
	requested := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if requested == "." || requested == "" {
		requested = "index.html"
	}
	if strings.HasPrefix(requested, ".") || strings.Contains(requested, "/.") {
		http.NotFound(w, r)
		return
	}
	content, stat, err := readWebUIFile(h.files, requested)
	if err != nil {
		if path.Ext(requested) != "" {
			http.NotFound(w, r)
			return
		}
		content, stat, err = readWebUIFile(h.files, "index.html")
	}
	if err != nil {
		http.Error(w, "UI is unavailable", http.StatusServiceUnavailable)
		return
	}
	// The content type and cache policy always describe the requested file, not
	// the compressed sibling that may end up carrying it on the wire.
	if contentType := mime.TypeByExtension(path.Ext(stat.Name())); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	setWebUICacheControl(w, stat.Name())
	if h.servePrecompressed(w, r, requested) {
		return
	}
	w.Header().Set("ETag", webUIFileETag(stat, ""))
	http.ServeContent(w, r, stat.Name(), stat.ModTime(), bytes.NewReader(content))
}

// servePrecompressed reports whether it answered the request with a
// build-time compressed sibling of the requested asset. The compressed bytes
// are served verbatim, which keeps brotli quality 11 out of the request path.
func (h webUIHandler) servePrecompressed(w http.ResponseWriter, r *http.Request, requested string) bool {
	for _, encoding := range precompressedEncodings {
		if !acceptsEncoding(r.Header.Get("Accept-Encoding"), encoding.name) {
			continue
		}
		content, stat, err := readWebUIFile(h.files, requested+"."+encoding.suffix)
		if err != nil {
			continue
		}
		etag := webUIFileETag(stat, encoding.name)
		w.Header().Set("Content-Encoding", encoding.name)
		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", stat.ModTime().UTC().Format(http.TimeFormat))
		if matchesETag(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return true
		}
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(content)
		}
		return true
	}
	return false
}

func setWebUICacheControl(w http.ResponseWriter, name string) {
	switch {
	case name == "index.html":
		w.Header().Set("Cache-Control", "no-cache")
	case strings.Contains(name, "-"):
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	default:
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
}

// webUIFileETag is stable for a given build (embedded files carry the build's
// modification time) and distinct per encoding, so `If-None-Match` revalidation
// of index.html costs a header round trip instead of a full body.
func webUIFileETag(stat fs.FileInfo, encoding string) string {
	tag := strconv.FormatInt(stat.Size(), 36) + "-" + strconv.FormatInt(stat.ModTime().UnixNano(), 36)
	if encoding != "" {
		tag += "-" + encoding
	}
	return `"` + tag + `"`
}

func matchesETag(header, etag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if candidate == "*" {
			return true
		}
		if strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}

// acceptsEncoding parses an Accept-Encoding header, honouring q=0 refusals and
// the `*` wildcard. An absent header means no encoding is acceptable.
func acceptsEncoding(header, name string) bool {
	wildcard := false
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		token := strings.ToLower(strings.TrimSpace(fields[0]))
		if token == "" {
			continue
		}
		quality := 1.0
		for _, parameter := range fields[1:] {
			parameter = strings.TrimSpace(parameter)
			value, ok := strings.CutPrefix(parameter, "q=")
			if !ok {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil {
				quality = 0
				break
			}
			quality = parsed
		}
		switch token {
		case name:
			// An explicit mention decides, including `q=0`: per RFC 9110 the most
			// specific match wins over the `*` wildcard.
			return quality > 0
		case "*":
			wildcard = quality > 0
		}
	}
	return wildcard
}

func readWebUIFile(files fs.FS, name string) ([]byte, fs.FileInfo, error) {
	file, err := files.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if stat.IsDir() {
		return nil, nil, fs.ErrNotExist
	}
	content, err := fs.ReadFile(files, name)
	if err != nil {
		return nil, nil, err
	}
	return content, stat, nil
}

func routeApplication(router chi.Router, apiServer http.Handler, ui http.Handler) {
	router.Handle("/api/*", http.StripPrefix("/api", apiServer))
	router.Handle("/v1/*", apiServer)
	router.Handle("/health/*", apiServer)
	if ui != nil {
		router.Handle("/*", ui)
		return
	}
	router.Handle("/*", apiServer)
}

var _ http.Handler = webUIHandler{}
