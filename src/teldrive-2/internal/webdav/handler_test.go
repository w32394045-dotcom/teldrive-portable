package webdav

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tgdrive/teldrive/v2/internal/catalog"
	"github.com/tgdrive/teldrive/v2/internal/db/sqlcgen"
	"github.com/tgdrive/teldrive/v2/internal/principal"
	"github.com/tgdrive/teldrive/v2/internal/transfer"
	"github.com/tgdrive/teldrive/v2/internal/uploads"
)

const (
	testUserID   = int64(7)
	testAPIKey   = "test-api-key"
	testSession  = "test-session-token"
	fixedModTime = int64(1_700_000_000)
)

var (
	folderID = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	fileID   = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	newID    = uuid.MustParse("33333333-3333-4333-8333-333333333333")
)

func fileRow(id uuid.UUID, name string, kind sqlcgen.FileKind, parent *uuid.UUID, size int64) *sqlcgen.File {
	row := &sqlcgen.File{
		ID:         pgtype.UUID{Bytes: id, Valid: true},
		UserID:     testUserID,
		Name:       name,
		Kind:       kind,
		Status:     sqlcgen.FileStatusActive,
		Generation: 1,
		ModTime:    pgtype.Timestamptz{Time: time.Unix(fixedModTime, 0).UTC(), Valid: true},
		MimeType:   pgtype.Text{String: "text/plain", Valid: true},
	}
	if kind == sqlcgen.FileKindFile {
		row.Size = pgtype.Int8{Int64: size, Valid: true}
	}
	if parent != nil {
		row.ParentID = pgtype.UUID{Bytes: *parent, Valid: true}
	}
	return row
}

// fakeCatalog is an in-memory stand-in for *catalog.Service. It implements only
// what the DAV layer calls, which keeps the tests free of PostgreSQL.
type fakeCatalog struct {
	mu       sync.Mutex
	rows     []*sqlcgen.File
	trashed  []uuid.UUID
	renamed  []string
	created  []string
	resolveN int
}

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{rows: []*sqlcgen.File{
		fileRow(folderID, "Documents", sqlcgen.FileKindFolder, nil, 0),
		fileRow(fileID, "notes.txt", sqlcgen.FileKindFile, nil, 5),
	}}
}

func (c *fakeCatalog) List(_ context.Context, in catalog.ListInput) ([]*sqlcgen.File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*sqlcgen.File
	for _, row := range c.rows {
		if row.UserID != in.UserID || row.Status != in.Status {
			continue
		}
		if !sameParent(row, in.ParentID) {
			continue
		}
		out = append(out, row)
	}
	if in.Limit > 0 && len(out) > int(in.Limit) {
		out = out[:int(in.Limit)]
	}
	return out, nil
}

func sameParent(row *sqlcgen.File, parent *uuid.UUID) bool {
	if parent == nil {
		return !row.ParentID.Valid
	}
	id, ok := googleUUID(row.ParentID)
	return ok && id == *parent
}

func googleUUID(value pgtype.UUID) (uuid.UUID, bool) {
	if !value.Valid {
		return uuid.Nil, false
	}
	return value.Bytes, true
}

func (c *fakeCatalog) Get(_ context.Context, userID int64, id uuid.UUID) (*sqlcgen.File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, row := range c.rows {
		if row.UserID == userID {
			if rowID, ok := googleUUID(row.ID); ok && rowID == id {
				return row, nil
			}
		}
	}
	return nil, catalog.ErrNotFound
}

func (c *fakeCatalog) ResolveFolderPath(_ context.Context, userID int64, _ *uuid.UUID, rawPath string) (*uuid.UUID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolveN++
	canonical, err := normalizePath(rawPath)
	if err != nil {
		return nil, catalog.ErrInvalidParent
	}
	if canonical == "/" {
		return nil, nil
	}
	parent := (*uuid.UUID)(nil)
	for _, segment := range strings.Split(strings.TrimPrefix(canonical, "/"), "/") {
		var found *sqlcgen.File
		for _, row := range c.rows {
			if row.UserID != userID || row.Kind != sqlcgen.FileKindFolder || row.Status != sqlcgen.FileStatusActive {
				continue
			}
			if row.Name == segment && sameParent(row, parent) {
				found = row
				break
			}
		}
		if found == nil {
			return nil, catalog.ErrInvalidParent
		}
		id, _ := googleUUID(found.ID)
		parent = &id
	}
	return parent, nil
}

func (c *fakeCatalog) CreateFolder(_ context.Context, in catalog.CreateFolderInput) (*sqlcgen.File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, row := range c.rows {
		if row.Name == in.Name && sameParent(row, in.ParentID) {
			return nil, catalog.ErrConflict
		}
	}
	c.created = append(c.created, in.Name)
	row := fileRow(newID, in.Name, sqlcgen.FileKindFolder, in.ParentID, 0)
	row.UserID = in.UserID
	c.rows = append(c.rows, row)
	return row, nil
}

func (c *fakeCatalog) Rename(_ context.Context, userID int64, id uuid.UUID, _ *int64, rawName string) (*sqlcgen.File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, row := range c.rows {
		if row.UserID == userID {
			if rowID, ok := googleUUID(row.ID); ok && rowID == id {
				c.renamed = append(c.renamed, rawName)
				row.Name = rawName
				return row, nil
			}
		}
	}
	return nil, catalog.ErrNotFound
}

func (c *fakeCatalog) Move(_ context.Context, userID int64, id uuid.UUID, parentID *uuid.UUID, _ *int64) (*sqlcgen.File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, row := range c.rows {
		if row.UserID == userID {
			if rowID, ok := googleUUID(row.ID); ok && rowID == id {
				if parentID == nil {
					row.ParentID = pgtype.UUID{}
				} else {
					row.ParentID = pgtype.UUID{Bytes: *parentID, Valid: true}
				}
				return row, nil
			}
		}
	}
	return nil, catalog.ErrNotFound
}

func (c *fakeCatalog) Trash(_ context.Context, userID int64, id uuid.UUID) (*sqlcgen.File, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, row := range c.rows {
		if row.UserID == userID {
			if rowID, ok := googleUUID(row.ID); ok && rowID == id {
				row.Status = sqlcgen.FileStatusTrashed
				c.trashed = append(c.trashed, id)
				return row, nil
			}
		}
	}
	return nil, catalog.ErrNotFound
}

type fakeUploads struct {
	mu       sync.Mutex
	parts    map[int32][]byte
	uploads  int
	complete int
	aborted  int
}

func newFakeUploads() *fakeUploads {
	return &fakeUploads{parts: map[int32][]byte{}}
}

func (u *fakeUploads) Create(_ context.Context, in uploads.CreateInput) (*sqlcgen.UploadSession, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.uploads++
	return &sqlcgen.UploadSession{
		ID:     pgtype.UUID{Bytes: in.ID, Valid: true},
		UserID: in.UserID,
		Name:   in.Name,
		State:  sqlcgen.UploadStateOpen,
		// One part is enough for the small bodies these tests upload.
		PartSize: in.ExpectedSize + 1,
	}, nil
}

func (u *fakeUploads) Complete(_ context.Context, _ int64, id uuid.UUID) (*sqlcgen.File, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.complete++
	return fileRow(id, "uploaded.txt", sqlcgen.FileKindFile, nil, int64(len(u.parts[1]))), nil
}

func (u *fakeUploads) Abort(_ context.Context, _ int64, id uuid.UUID) (*sqlcgen.UploadSession, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.aborted++
	return &sqlcgen.UploadSession{ID: pgtype.UUID{Bytes: id, Valid: true}, State: sqlcgen.UploadStateAborted}, nil
}

type fakePipeline struct {
	mu      sync.Mutex
	parts   map[int32][]byte
	uploads *fakeUploads
}

func (p *fakePipeline) UploadPart(_ context.Context, request transfer.UploadPartRequest) (*transfer.UploadPartResult, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.parts[request.PartNo] = body
	if p.uploads != nil {
		p.uploads.parts[request.PartNo] = body
	}
	return &transfer.UploadPartResult{}, nil
}

type memoryReader struct{ *bytes.Reader }

func (memoryReader) Close() error { return nil }

type fakeDownloader struct{ content []byte }

func (d fakeDownloader) Open(_ context.Context, _ transfer.DownloadRequest) (*transfer.Download, error) {
	return &transfer.Download{
		Reader:      memoryReader{bytes.NewReader(d.content)},
		TotalSize:   int64(len(d.content)),
		Length:      int64(len(d.content)),
		ContentType: "text/plain",
	}, nil
}

type fakeAuth struct {
	accepted map[string]principal.Identity
}

func (a fakeAuth) AuthenticateAPIKey(_ context.Context, raw string) (principal.Identity, error) {
	if identity, ok := a.accepted[raw]; ok {
		return identity, nil
	}
	return principal.Identity{}, errors.New("invalid api key")
}

func (a fakeAuth) AuthenticateBearer(_ context.Context, raw string) (principal.Identity, error) {
	if identity, ok := a.accepted[raw]; ok {
		return identity, nil
	}
	return principal.Identity{}, errors.New("invalid session")
}

func testHandler(t *testing.T, enabled bool) (*Handler, *fakeCatalog, *fakeUploads, *fakePipeline, *ConfigStore) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "webdav.json")
	store := NewConfigStore(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := store.Load(); err != nil {
		t.Fatalf("load store: %v", err)
	}
	if enabled {
		if err := store.SetEnabled(true); err != nil {
			t.Fatalf("enable: %v", err)
		}
	}
	rows := newFakeCatalog()
	uploads := newFakeUploads()
	pipeline := &fakePipeline{parts: map[int32][]byte{}, uploads: uploads}
	auth := fakeAuth{accepted: map[string]principal.Identity{
		testAPIKey:   {UserID: testUserID, Roles: []string{"user", "admin", "owner"}},
		testSession:  {UserID: testUserID, Roles: []string{"user", "admin", "owner"}},
		"plain-user": {UserID: 9, Roles: []string{"user"}},
	}}
	handler, err := New(Options{
		Catalog:     rows,
		Uploads:     uploads,
		Pipeline:    pipeline,
		Downloader:  fakeDownloader{content: []byte("hello")},
		Config:      store,
		APIKeys:     auth,
		Browser:     auth,
		TempDir:     t.TempDir(),
		HTTPAddress: "0.0.0.0:8080",
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}
	return handler, rows, uploads, pipeline, store
}

func davRequest(method, target string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.SetBasicAuth("anyone", testAPIKey)
	return request
}

func TestServeDAVRefusesWhenDisabled(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := testHandler(t, false)
	response := httptest.NewRecorder()
	handler.ServeDAV(response, davRequest("PROPFIND", "/webdav/", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 while disabled", response.Code)
	}
	if !strings.Contains(response.Body.String(), "Settings") {
		t.Fatalf("body should point at the setting: %q", response.Body.String())
	}
}

func TestServeDAVRequiresCredentials(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := testHandler(t, true)

	response := httptest.NewRecorder()
	handler.ServeDAV(response, httptest.NewRequest("PROPFIND", "/webdav/", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without credentials", response.Code)
	}
	if !strings.HasPrefix(response.Header().Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("missing Basic challenge: %q", response.Header().Get("WWW-Authenticate"))
	}

	request := httptest.NewRequest("PROPFIND", "/webdav/", nil)
	request.SetBasicAuth("anyone", "wrong-key")
	response = httptest.NewRecorder()
	handler.ServeDAV(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a bad key", response.Code)
	}
}

func TestServeDAVAcceptsKeyInEitherBasicField(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ user, pass string }{
		{"anyone", testAPIKey},
		{testAPIKey, ""},
	} {
		handler, _, _, _, _ := testHandler(t, true)
		request := httptest.NewRequest("PROPFIND", "/webdav/", nil)
		request.SetBasicAuth(test.user, test.pass)
		request.Header.Set("Depth", "1")
		response := httptest.NewRecorder()
		handler.ServeDAV(response, request)
		if response.Code != http.StatusMultiStatus {
			t.Fatalf("user=%q pass=%q status = %d, want 207", test.user, test.pass, response.Code)
		}
	}
}

func TestPropfindListsTheDriveRoot(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := testHandler(t, true)
	request := davRequest("PROPFIND", "/webdav/", nil)
	request.Header.Set("Depth", "1")
	response := httptest.NewRecorder()
	handler.ServeDAV(response, request)

	if response.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{"Documents", "notes.txt", "getcontentlength"} {
		if !strings.Contains(body, want) {
			t.Errorf("PROPFIND response does not contain %q", want)
		}
	}
}

func TestGetStreamsTheFileContents(t *testing.T) {
	t.Parallel()
	handler, _, _, _, _ := testHandler(t, true)
	response := httptest.NewRecorder()
	handler.ServeDAV(response, davRequest("GET", "/webdav/notes.txt", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if got := response.Body.String(); got != "hello" {
		t.Fatalf("body = %q, want %q", got, "hello")
	}
}

func TestMkcolCreatesAFolderAndRejectsMissingParent(t *testing.T) {
	t.Parallel()
	handler, rows, _, _, _ := testHandler(t, true)

	response := httptest.NewRecorder()
	handler.ServeDAV(response, davRequest("MKCOL", "/webdav/New%20folder", nil))
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", response.Code, response.Body.String())
	}
	if len(rows.created) != 1 || rows.created[0] != "New folder" {
		t.Fatalf("created = %v, want [New folder]", rows.created)
	}

	// A missing intermediate folder must not be created implicitly.
	response = httptest.NewRecorder()
	handler.ServeDAV(response, davRequest("MKCOL", "/webdav/missing/child", nil))
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for a missing parent", response.Code)
	}
}

func TestDeleteMovesToTrash(t *testing.T) {
	t.Parallel()
	handler, rows, _, _, _ := testHandler(t, true)
	response := httptest.NewRecorder()
	handler.ServeDAV(response, davRequest("DELETE", "/webdav/notes.txt", nil))
	if response.Code != http.StatusNoContent && response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 204/200: %s", response.Code, response.Body.String())
	}
	if len(rows.trashed) != 1 || rows.trashed[0] != fileID {
		t.Fatalf("trashed = %v, want the file id", rows.trashed)
	}
}

func TestMoveRenamesWithinTheSameFolder(t *testing.T) {
	t.Parallel()
	handler, rows, _, _, _ := testHandler(t, true)
	request := davRequest("MOVE", "/webdav/notes.txt", nil)
	request.Header.Set("Destination", "/webdav/renamed.txt")
	response := httptest.NewRecorder()
	handler.ServeDAV(response, request)

	if response.Code != http.StatusCreated && response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 201/204: %s", response.Code, response.Body.String())
	}
	if len(rows.renamed) != 1 || rows.renamed[0] != "renamed.txt" {
		t.Fatalf("renamed = %v, want [renamed.txt]", rows.renamed)
	}
}

func TestPutUploadsTheBody(t *testing.T) {
	t.Parallel()
	handler, _, uploads, pipeline, _ := testHandler(t, true)
	request := davRequest("PUT", "/webdav/uploaded.txt", strings.NewReader("hello"))
	request.Header.Set("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	handler.ServeDAV(response, request)

	if response.Code != http.StatusCreated && response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 201/204: %s", response.Code, response.Body.String())
	}
	if uploads.uploads != 1 || uploads.complete != 1 {
		t.Fatalf("uploads=%d complete=%d, want 1/1", uploads.uploads, uploads.complete)
	}
	if got := string(pipeline.parts[1]); got != "hello" {
		t.Fatalf("stored part = %q, want %q", got, "hello")
	}
}

func TestConfigEndpointHonoursSessionAndRole(t *testing.T) {
	t.Parallel()
	handler, _, _, _, store := testHandler(t, false)

	// No cookie at all.
	response := httptest.NewRecorder()
	handler.ServeConfig(response, httptest.NewRequest("GET", "/webdav-config", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a session", response.Code)
	}

	// A signed-in user without an admin role.
	request := httptest.NewRequest("PUT", "/webdav-config", strings.NewReader(`{"enabled":true}`))
	request.AddCookie(&http.Cookie{Name: accessCookieName, Value: "plain-user"})
	response = httptest.NewRecorder()
	handler.ServeConfig(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-admin", response.Code)
	}

	// An owner can read and change it.
	request = httptest.NewRequest("GET", "/webdav-config", nil)
	request.AddCookie(&http.Cookie{Name: accessCookieName, Value: testSession})
	response = httptest.NewRecorder()
	handler.ServeConfig(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"enabled":false`) {
		t.Fatalf("body = %s, want enabled false", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `/webdav"`) {
		t.Fatalf("body = %s, want a copyable /webdav URL", response.Body.String())
	}

	request = httptest.NewRequest("PUT", "/webdav-config", strings.NewReader(`{"enabled":true}`))
	request.AddCookie(&http.Cookie{Name: accessCookieName, Value: testSession})
	response = httptest.NewRecorder()
	handler.ServeConfig(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if !store.Enabled() {
		t.Fatal("store was not enabled")
	}

	// A malformed body must not flip anything.
	request = httptest.NewRequest("PUT", "/webdav-config", strings.NewReader(`{"enabled":`))
	request.AddCookie(&http.Cookie{Name: accessCookieName, Value: testSession})
	response = httptest.NewRecorder()
	handler.ServeConfig(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for a malformed body", response.Code)
	}
	if !store.Enabled() {
		t.Fatal("a malformed body changed the setting")
	}
}

func TestConfigStorePersistsAndDegradesSafely(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "webdav.json")

	store := NewConfigStore(path, nil)
	if err := store.Load(); err != nil {
		t.Fatalf("missing file must not be an error: %v", err)
	}
	if store.Enabled() {
		t.Fatal("WebDAV must default to disabled")
	}
	if err := store.SetEnabled(true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	reloaded := NewConfigStore(path, nil)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Enabled() {
		t.Fatal("enabled state did not survive a reload")
	}

	// A corrupt file must not stop the server from starting.
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := NewConfigStore(path, nil)
	if err := broken.Load(); err != nil {
		t.Fatalf("malformed file should degrade, not fail: %v", err)
	}
	if broken.Enabled() {
		t.Fatal("a malformed file must degrade to disabled")
	}
}
