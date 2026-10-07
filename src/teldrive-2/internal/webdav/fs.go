package webdav

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	xwebdav "golang.org/x/net/webdav"

	"github.com/tgdrive/teldrive/v2/internal/catalog"
	"github.com/tgdrive/teldrive/v2/internal/db/sqlcgen"
	"github.com/tgdrive/teldrive/v2/internal/dbtypes"
	"github.com/tgdrive/teldrive/v2/internal/transfer"
	"github.com/tgdrive/teldrive/v2/internal/uploads"
)

const (
	// listPageSize is the maximum page size accepted by catalog.List.
	listPageSize = 500
	// maxListPages bounds directory pagination so a pathological folder cannot
	// keep a PROPFIND running forever.
	maxListPages = 40
)

// Catalog is the catalog slice the DAV file system needs. It is satisfied by
// *catalog.Service so the DAV layer reuses the same authorization, conflict, and
// cache-invalidation rules as the HTTP API instead of issuing SQL itself.
type Catalog interface {
	Get(ctx context.Context, userID int64, fileID uuid.UUID) (*sqlcgen.File, error)
	List(ctx context.Context, in catalog.ListInput) ([]*sqlcgen.File, error)
	ResolveFolderPath(ctx context.Context, userID int64, rootID *uuid.UUID, rawPath string) (*uuid.UUID, error)
	CreateFolder(ctx context.Context, in catalog.CreateFolderInput) (*sqlcgen.File, error)
	Rename(ctx context.Context, userID int64, fileID uuid.UUID, expectedGeneration *int64, rawName string) (*sqlcgen.File, error)
	Move(ctx context.Context, userID int64, fileID uuid.UUID, parentID *uuid.UUID, expectedGeneration *int64) (*sqlcgen.File, error)
	Trash(ctx context.Context, userID int64, fileID uuid.UUID) (*sqlcgen.File, error)
}

// UploadService is the upload-session boundary; satisfied by *uploads.Service.
type UploadService interface {
	Create(ctx context.Context, in uploads.CreateInput) (*sqlcgen.UploadSession, error)
	Complete(ctx context.Context, userID int64, uploadID uuid.UUID) (*sqlcgen.File, error)
	Abort(ctx context.Context, userID int64, uploadID uuid.UUID) (*sqlcgen.UploadSession, error)
}

// PartUploader streams one upload part to storage; satisfied by *transfer.Pipeline.
type PartUploader interface {
	UploadPart(ctx context.Context, request transfer.UploadPartRequest) (*transfer.UploadPartResult, error)
}

// Downloader opens a streaming, seekable read of a stored file; satisfied by
// *transfer.Downloader.
type Downloader interface {
	Open(ctx context.Context, request transfer.DownloadRequest) (*transfer.Download, error)
}

// requestMeta carries the few request facts the webdav.FileSystem interface
// cannot deliver: OpenFile only receives a context, but PUT needs the declared
// body size and a way to tell a complete body from a truncated one.
type requestMeta struct {
	contentLength int64
	body          *bodyTracker
}

type requestMetaKey struct{}

func withRequestMeta(ctx context.Context, meta requestMeta) context.Context {
	return context.WithValue(ctx, requestMetaKey{}, meta)
}

func requestMetaFromContext(ctx context.Context) requestMeta {
	meta, _ := ctx.Value(requestMetaKey{}).(requestMeta)
	return meta
}

// bodyTracker records whether the request body ended cleanly. A PUT whose body
// is cut short must not publish a partial file, and the webdav handler reports
// the copy error separately from Close, so the fact has to be captured while
// the body is being read.
type bodyTracker struct {
	clean atomic.Bool
}

func (t *bodyTracker) markClean() {
	if t != nil {
		t.clean.Store(true)
	}
}

// Clean reports whether the body reader reached EOF without an error.
func (t *bodyTracker) Clean() bool {
	return t == nil || t.clean.Load()
}

type trackingBody struct {
	io.ReadCloser
	tracker *bodyTracker
}

func (b *trackingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if errors.Is(err, io.EOF) {
		b.tracker.markClean()
	}
	return n, err
}

// failureHolder records the first domain failure of a DAV request. webdav.Handler
// maps nearly every write failure onto 405 and every OpenFile failure onto 404,
// which would hide the real reason; the response wrapper consults this holder to
// answer with a status the client can act on.
type failureHolder struct {
	mu  sync.Mutex
	err error
}

func (h *failureHolder) record(err error) {
	if h == nil || err == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err == nil {
		h.err = err
	}
}

func (h *failureHolder) Err() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

// node is one addressable DAV resource: the drive root (file == nil) or a
// catalog row.
type node struct {
	path string
	file *sqlcgen.File
}

func (n node) isDir() bool {
	return n.file == nil || n.file.Kind == sqlcgen.FileKindFolder
}

func (n node) id() (uuid.UUID, bool) {
	if n.file == nil {
		return uuid.Nil, false
	}
	return dbtypes.GoogleUUID(n.file.ID)
}

func (n node) name() string {
	if n.file == nil {
		return "/"
	}
	return n.file.Name
}

func (n node) size() int64 {
	if n.file == nil || !n.file.Size.Valid || n.file.Size.Int64 < 0 {
		return 0
	}
	return n.file.Size.Int64
}

func (n node) modTime() time.Time {
	if n.file == nil || !n.file.ModTime.Valid {
		return time.Unix(0, 0).UTC()
	}
	return n.file.ModTime.Time.UTC()
}

func (n node) info() os.FileInfo {
	if n.file == nil {
		return davFileInfo{name: "/", dir: true, modTime: time.Unix(0, 0).UTC()}
	}
	return davFileInfo{
		name:        n.file.Name,
		size:        n.size(),
		dir:         n.isDir(),
		modTime:     n.modTime(),
		etag:        fmt.Sprintf(`"%x-%x"`, n.file.Generation, n.size()),
		contentType: rowContentType(n.file),
	}
}

// fileSystem is a per-request view of one user's drive. Building it per request
// keeps the authenticated principal out of shared state.
type fileSystem struct {
	meta       requestMeta
	userID     int64
	catalog    Catalog
	uploads    UploadService
	pipeline   PartUploader
	downloader Downloader
	partSize   int64
	tempDir    string
	failures   *failureHolder
}

var _ xwebdav.FileSystem = (*fileSystem)(nil)

func (fs *fileSystem) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	resolved, err := fs.resolve(ctx, name)
	if err != nil {
		return nil, err
	}
	return resolved.info(), nil
}

func (fs *fileSystem) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (xwebdav.File, error) {
	canonical, err := normalizePath(name)
	if err != nil {
		return nil, notExistError(name)
	}
	if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
		return fs.openForWrite(ctx, name, canonical)
	}
	resolved, err := fs.resolve(ctx, canonical)
	if err != nil {
		return nil, err
	}
	if resolved.isDir() {
		return &davDirectoryFile{fs: fs, node: resolved, ctx: ctx}, nil
	}
	return &davReadFile{fs: fs, node: resolved, ctx: ctx}, nil
}

// Mkdir creates a folder. A missing parent must surface as fs.ErrNotExist so
// webdav answers MKCOL with 409 Conflict instead of a generic failure.
func (fs *fileSystem) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	canonical, err := normalizePath(name)
	if err != nil {
		return ErrInvalidPath
	}
	if canonical == "/" {
		return existError("mkdir", name)
	}
	if _, err := fs.resolve(ctx, canonical); err == nil {
		return existError("mkdir", name)
		// The receiver is named fs, which shadows the io/fs package here;
		// os.ErrNotExist is the same value.
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parentPath, element, err := splitParent(canonical)
	if err != nil {
		return err
	}
	parentID, err := fs.folderID(ctx, parentPath)
	if err != nil {
		return err
	}
	if _, err := fs.catalog.CreateFolder(ctx, catalog.CreateFolderInput{
		UserID: fs.userID, ParentID: parentID, Name: element,
	}); err != nil {
		if errors.Is(err, catalog.ErrConflict) {
			return existError("mkdir", name)
		}
		return fmt.Errorf("create folder: %w", err)
	}
	return nil
}

// RemoveAll deletes a resource and its subtree. Teldrive's delete is a move to
// trash, so a DAV DELETE is recoverable in the UI (and by RESTORE) rather than a
// permanent purge.
func (fs *fileSystem) RemoveAll(ctx context.Context, name string) error {
	canonical, err := normalizePath(name)
	if err != nil {
		return ErrInvalidPath
	}
	if canonical == "/" {
		return ErrRootOperation
	}
	resolved, err := fs.resolve(ctx, canonical)
	if errors.Is(err, os.ErrNotExist) {
		// os.RemoveAll semantics: a missing target is not an error.
		return nil
	}
	if err != nil {
		return err
	}
	fileID, ok := resolved.id()
	if !ok {
		return ErrRootOperation
	}
	if _, err := fs.catalog.Trash(ctx, fs.userID, fileID); err != nil {
		if errors.Is(err, catalog.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("trash file: %w", err)
	}
	return nil
}

// Rename implements MOVE for both a rename inside one folder and a cross-folder
// move: the catalog exposes one service call for each half, so a MOVE that
// changes both parent and name performs both. If the second half fails the entry
// is left moved-but-not-renamed, which the caller sees as a failed MOVE.
func (fs *fileSystem) Rename(ctx context.Context, oldName, newName string) error {
	source, err := fs.resolve(ctx, oldName)
	if err != nil {
		return err
	}
	destination, err := normalizePath(newName)
	if err != nil {
		return ErrInvalidPath
	}
	if destination == "/" {
		return ErrRootOperation
	}
	if source.path == destination {
		return nil
	}
	sourceParent, sourceName, err := splitParent(source.path)
	if err != nil {
		return err
	}
	destinationParent, destinationName, err := splitParent(destination)
	if err != nil {
		return err
	}
	fileID, ok := source.id()
	if !ok {
		return ErrRootOperation
	}
	sourceParentID, err := fs.folderID(ctx, sourceParent)
	if err != nil {
		return err
	}
	destinationParentID, err := fs.folderID(ctx, destinationParent)
	if err != nil {
		return err
	}
	if !sameFolder(sourceParentID, destinationParentID) {
		if _, err := fs.catalog.Move(ctx, fs.userID, fileID, destinationParentID, nil); err != nil {
			return classifyWriteError("move file", newName, err)
		}
	}
	if sourceName != destinationName {
		if _, err := fs.catalog.Rename(ctx, fs.userID, fileID, nil, destinationName); err != nil {
			return classifyWriteError("rename file", newName, err)
		}
	}
	return nil
}

func (fs *fileSystem) openForWrite(ctx context.Context, name, canonical string) (xwebdav.File, error) {
	if canonical == "/" {
		err := ErrRootOperation
		fs.failures.record(err)
		return nil, err
	}
	parentPath, element, err := splitParent(canonical)
	if err != nil {
		return nil, err
	}
	parentID, err := fs.folderID(ctx, parentPath)
	if err != nil {
		return nil, err
	}
	if existing, err := fs.resolve(ctx, canonical); err == nil && existing.isDir() {
		conflict := existError("open", name)
		fs.failures.record(conflict)
		return nil, conflict
	}
	return newUploadFile(ctx, fs, parentID, element), nil
}

// resolve maps a DAV path onto a catalog node. Missing resources always come
// back as fs.ErrNotExist, which is what makes webdav answer 404 (GET) or 409
// (MKCOL with a missing parent) instead of 500.
func (fs *fileSystem) resolve(ctx context.Context, name string) (node, error) {
	canonical, err := normalizePath(name)
	if err != nil {
		return node{}, notExistError(name)
	}
	if canonical == "/" {
		return node{path: "/"}, nil
	}
	parentPath, element, err := splitParent(canonical)
	if err != nil {
		return node{}, notExistError(name)
	}
	parentID, err := fs.folderID(ctx, parentPath)
	if err != nil {
		return node{}, err
	}
	children, err := fs.children(ctx, parentID)
	if err != nil {
		return node{}, err
	}
	for _, child := range children {
		if child != nil && child.Name == element {
			return node{path: canonical, file: child}, nil
		}
	}
	return node{}, notExistError(name)
}

// folderID resolves a canonical folder path to its catalog id, with nil meaning
// the drive root. Resolving through the catalog keeps path traversal in one
// place: a path that escapes the tree cannot be expressed as a folder id.
func (fs *fileSystem) folderID(ctx context.Context, canonical string) (*uuid.UUID, error) {
	if canonical == "/" || canonical == "" {
		return nil, nil
	}
	id, err := fs.catalog.ResolveFolderPath(ctx, fs.userID, nil, canonical)
	if err != nil {
		if errors.Is(err, catalog.ErrInvalidParent) {
			return nil, notExistError(canonical)
		}
		return nil, fmt.Errorf("resolve folder path: %w", err)
	}
	return id, nil
}

// children lists every active entry of one folder, following the catalog's
// (name, id) cursor so directories larger than one page are still complete.
func (fs *fileSystem) children(ctx context.Context, parentID *uuid.UUID) ([]*sqlcgen.File, error) {
	var (
		items     []*sqlcgen.File
		afterName string
		afterID   *uuid.UUID
	)
	for range maxListPages {
		batch, err := fs.catalog.List(ctx, catalog.ListInput{
			UserID: fs.userID, Scope: "folder", ParentID: parentID,
			Status: sqlcgen.FileStatusActive, Limit: listPageSize,
			AfterName: afterName, AfterID: afterID,
		})
		if err != nil {
			return nil, fmt.Errorf("list drive folder: %w", err)
		}
		items = append(items, batch...)
		if len(batch) < listPageSize {
			return items, nil
		}
		last := batch[len(batch)-1]
		id, ok := dbtypes.GoogleUUID(last.ID)
		if !ok {
			return items, nil
		}
		afterName, afterID = last.Name, &id
	}
	return items, nil
}

// upload streams a complete file into storage as one upload session with as many
// parts as the size requires. The reader is consumed exactly once and in order,
// which is what lets a PUT body be piped straight through.
func (fs *fileSystem) upload(ctx context.Context, parentID *uuid.UUID, name string, body io.Reader, size int64) error {
	if size < 0 {
		return ErrUnknownSize
	}
	session, err := fs.uploads.Create(ctx, uploads.CreateInput{
		UserID: fs.userID, ParentID: parentID, Name: name, ExpectedSize: size,
		MIMEType: optionalString(mime.TypeByExtension(path.Ext(name))),
		ModTime:  time.Now().UTC(), ConflictPolicy: sqlcgen.NameConflictPolicyReplace,
		PartSize: fs.partSize,
	})
	if err != nil {
		return fs.classifyUploadError(err)
	}
	uploadID, ok := dbtypes.GoogleUUID(session.ID)
	if !ok {
		return errors.New("webdav: upload session id is invalid")
	}
	partSize := session.PartSize
	if partSize <= 0 {
		partSize = fs.partSize
	}
	parts := int32(0)
	if size > 0 && partSize > 0 {
		parts = int32((size + partSize - 1) / partSize)
	}
	for part := int32(1); part <= parts; part++ {
		offset := int64(part-1) * partSize
		plainSize := min(partSize, size-offset)
		if _, err := fs.pipeline.UploadPart(ctx, transfer.UploadPartRequest{
			UserID: fs.userID, UploadID: uploadID, PartNo: part,
			PlainSize: plainSize,
			// Each part reads at most its own size, so the next part still starts
			// on the byte boundary the stream is at.
			Body: io.LimitReader(body, plainSize),
		}); err != nil {
			fs.abortUpload(ctx, uploadID)
			return fs.classifyUploadError(err)
		}
	}
	file, err := fs.uploads.Complete(ctx, fs.userID, uploadID)
	if err != nil {
		fs.abortUpload(ctx, uploadID)
		return fs.classifyUploadError(err)
	}
	if file == nil {
		return errors.New("webdav: upload completed without a file")
	}
	return nil
}

// abortUpload releases a session that will never be completed. The caller
// already has a more specific error, so a failed abort is only logged by the
// caller's error path, never surfaced instead of it.
func (fs *fileSystem) abortUpload(ctx context.Context, uploadID uuid.UUID) {
	abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = fs.uploads.Abort(abortCtx, fs.userID, uploadID)
}

func (fs *fileSystem) classifyUploadError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, catalog.ErrInvalidParent), errors.Is(err, uploads.ErrInvalidParent):
		return notExistError("upload")
	case errors.Is(err, uploads.ErrNameConflict), errors.Is(err, catalog.ErrConflict), errors.Is(err, catalog.ErrCycle):
		return existError("put", "upload")
	default:
		return err
	}
}

func classifyWriteError(action, name string, err error) error {
	switch {
	case errors.Is(err, catalog.ErrConflict):
		return existError(action, name)
	case errors.Is(err, catalog.ErrNotFound):
		return notExistError(name)
	default:
		return fmt.Errorf("%s: %w", action, err)
	}
}

func notExistError(name string) error {
	return &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
}

func existError(op, name string) error {
	return &fs.PathError{Op: op, Path: name, Err: fs.ErrExist}
}

func sameFolder(left, right *uuid.UUID) bool {
	switch {
	case left == nil && right == nil:
		return true
	case left == nil || right == nil:
		return false
	default:
		return *left == *right
	}
}

func rowContentType(file *sqlcgen.File) string {
	if file != nil && file.MimeType.Valid {
		if parsed, _, err := mime.ParseMediaType(file.MimeType.String); err == nil && parsed != "" {
			return parsed
		}
	}
	if file != nil {
		if inferred := mime.TypeByExtension(path.Ext(file.Name)); inferred != "" {
			if parsed, _, err := mime.ParseMediaType(inferred); err == nil && parsed != "" {
				return parsed
			}
		}
	}
	return "application/octet-stream"
}

func optionalString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
