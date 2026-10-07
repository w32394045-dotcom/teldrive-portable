package webdav

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	xwebdav "golang.org/x/net/webdav"

	"github.com/tgdrive/teldrive/v2/internal/transfer"
)

// defaultPartSize is the upload part size used for WebDAV PUTs. It matches the
// upload service default: one catalog part per 512 MiB, so a single PUT stays
// within the same storage layout the rest of the server produces.
const defaultPartSize int64 = 512 * 1024 * 1024

// spoolRequired reports whether a PUT body has to be staged on disk before it
// can be uploaded. The upload pipeline needs the final size up front to plan its
// parts, and a chunked request carries no Content-Length, so only the size-less
// case pays for a temporary file; a sized body streams straight through a pipe.
func spoolRequired(contentLength int64) bool {
	return contentLength < 0
}

// davFileInfo is the os.FileInfo implementation shared by every DAV resource.
// It also implements the optional webdav ETager and ContentTyper interfaces,
// which matters for more than ETags: without ContentType, a PROPFIND that asks
// for all properties makes webdav open and read 512 bytes of every listed file,
// and here that would start a Telegram download per entry.
type davFileInfo struct {
	name        string
	size        int64
	modTime     time.Time
	dir         bool
	etag        string
	contentType string
}

func (i davFileInfo) Name() string       { return i.name }
func (i davFileInfo) Size() int64        { return i.size }
func (i davFileInfo) ModTime() time.Time { return i.modTime }
func (i davFileInfo) IsDir() bool        { return i.dir }
func (i davFileInfo) Sys() any           { return nil }

func (i davFileInfo) Mode() os.FileMode {
	if i.dir {
		return os.ModeDir | 0o755
	}
	return 0o644
}

// ETag returns the catalog generation-based validator, or ErrNotImplemented for
// the virtual root so webdav falls back to its size/modtime heuristic.
func (i davFileInfo) ETag(context.Context) (string, error) {
	if i.etag == "" {
		return "", xwebdav.ErrNotImplemented
	}
	return i.etag, nil
}

func (i davFileInfo) ContentType(context.Context) (string, error) {
	if i.contentType == "" {
		return "", xwebdav.ErrNotImplemented
	}
	return i.contentType, nil
}

var (
	_ os.FileInfo          = davFileInfo{}
	_ xwebdav.ETager       = davFileInfo{}
	_ xwebdav.ContentTyper = davFileInfo{}
)

// davReadFile streams one stored file. The download is opened lazily on the
// first Read so HEAD and PROPFIND never start a Telegram session, and seeks only
// move the position until a reader exists.
type davReadFile struct {
	fs   *fileSystem
	node node
	ctx  context.Context

	mu     sync.Mutex
	pos    int64
	reader io.ReadSeekCloser
	closed bool
}

func (f *davReadFile) Stat() (os.FileInfo, error) {
	return f.node.info(), nil
}

func (f *davReadFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, fs.ErrClosed
	}
	if f.reader == nil {
		reader, err := f.open()
		if err != nil {
			return 0, err
		}
		f.reader = reader
	}
	return f.reader.Read(p)
}

// Seek implements io.Seeker for http.ServeContent, including the
// Seek(0, io.SeekEnd) size probe it performs before sending a body.
func (f *davReadFile) Seek(offset int64, whence int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, fs.ErrClosed
	}
	size := f.node.size()
	var absolute int64
	switch whence {
	case io.SeekStart:
		absolute = offset
	case io.SeekCurrent:
		absolute = f.pos + offset
	case io.SeekEnd:
		absolute = size + offset
	default:
		return f.pos, errors.New("webdav: invalid seek whence")
	}
	if absolute < 0 {
		absolute = 0
	}
	if absolute > size {
		absolute = size
	}
	f.pos = absolute
	if f.reader != nil {
		if _, err := f.reader.Seek(absolute, io.SeekStart); err != nil {
			return f.pos, err
		}
	}
	return f.pos, nil
}

func (f *davReadFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	if f.reader != nil {
		err := f.reader.Close()
		f.reader = nil
		return err
	}
	return nil
}

func (f *davReadFile) Readdir(int) ([]os.FileInfo, error) {
	return nil, &fs.PathError{Op: "readdir", Path: f.node.path, Err: fs.ErrInvalid}
}

func (f *davReadFile) Write([]byte) (int, error) {
	return 0, &fs.PathError{Op: "write", Path: f.node.path, Err: fs.ErrPermission}
}

// open starts a full-file download and positions it at the current offset. The
// reader is always opened for the whole file so that its own SeekEnd reports the
// real file size; the offset is applied with a seek afterwards.
func (f *davReadFile) open() (io.ReadSeekCloser, error) {
	fileID, ok := f.node.id()
	if !ok {
		return nil, notExistError(f.node.path)
	}
	download, err := f.fs.downloader.Open(f.ctx, transfer.DownloadRequest{
		UserID: f.fs.userID, FileID: fileID, Offset: 0, Length: -1,
	})
	if err != nil {
		return nil, err
	}
	if f.pos > 0 {
		if _, err := download.Reader.Seek(f.pos, io.SeekStart); err != nil {
			_ = download.Reader.Close()
			return nil, err
		}
	}
	return download.Reader, nil
}

// davDirectoryFile serves Readdir for one collection. Children are listed on
// demand and then served from the cached slice, matching the http.File
// contract that repeated Readdir calls continue where the last one stopped.
type davDirectoryFile struct {
	fs   *fileSystem
	node node
	ctx  context.Context

	mu       sync.Mutex
	children []os.FileInfo
	offset   int
	closed   bool
}

func (f *davDirectoryFile) Stat() (os.FileInfo, error) {
	return f.node.info(), nil
}

func (f *davDirectoryFile) Readdir(count int) ([]os.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, fs.ErrClosed
	}
	if f.children == nil {
		parentID, _ := f.node.id()
		var parent *uuid.UUID
		if f.node.file != nil {
			parent = &parentID
		}
		rows, err := f.fs.children(f.ctx, parent)
		if err != nil {
			return nil, err
		}
		children := make([]os.FileInfo, 0, len(rows))
		for _, row := range rows {
			if row == nil {
				continue
			}
			child := node{path: joinElement(f.node.path, row.Name), file: row}
			children = append(children, child.info())
		}
		f.children = children
	}
	if count <= 0 {
		remaining := f.children[f.offset:]
		f.offset = len(f.children)
		return remaining, nil
	}
	if f.offset >= len(f.children) {
		return nil, io.EOF
	}
	end := min(f.offset+count, len(f.children))
	page := f.children[f.offset:end]
	f.offset = end
	return page, nil
}

func (f *davDirectoryFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: f.node.path, Err: fs.ErrInvalid}
}

func (f *davDirectoryFile) Seek(int64, int) (int64, error) {
	return 0, &fs.PathError{Op: "seek", Path: f.node.path, Err: fs.ErrInvalid}
}

func (f *davDirectoryFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *davDirectoryFile) Write([]byte) (int, error) {
	return 0, &fs.PathError{Op: "write", Path: f.node.path, Err: fs.ErrPermission}
}

// uploadFile receives a PUT body and publishes it as one catalog file.
//
// A known Content-Length streams through an in-memory pipe into the upload
// pipeline, so a multi-gigabyte upload never sits in RAM and never touches disk.
// An unknown length (chunked request) is staged in a temporary file first, which
// is the only way to learn the size the pipeline needs; the temporary file is
// removed as soon as the upload finishes. Publishing only happens in Close, so a
// failed transfer leaves the previous file at that path untouched.
type uploadFile struct {
	ctx      context.Context
	fs       *fileSystem
	parentID *uuid.UUID
	name     string
	size     int64

	mu      sync.Mutex
	spool   *os.File
	spooled int64
	written int64
	started bool
	closed  bool
	pipe    *io.PipeWriter
	done    chan error
	result  error
}

func newUploadFile(ctx context.Context, fs *fileSystem, parentID *uuid.UUID, name string) *uploadFile {
	return &uploadFile{
		ctx: ctx, fs: fs, parentID: parentID, name: name,
		size: requestMetaFromContext(ctx).contentLength,
	}
}

func (f *uploadFile) Stat() (os.FileInfo, error) {
	return davFileInfo{name: f.name, size: f.written, modTime: time.Now().UTC()}, nil
}

func (f *uploadFile) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, fs.ErrClosed
	}
	if spoolRequired(f.size) {
		return f.writeSpool(p)
	}
	f.startStream()
	written, err := f.pipe.Write(p)
	f.written += int64(written)
	return written, err
}

func (f *uploadFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: f.name, Err: fs.ErrInvalid}
}

func (f *uploadFile) Seek(int64, int) (int64, error) {
	return 0, &fs.PathError{Op: "seek", Path: f.name, Err: fs.ErrInvalid}
}

func (f *uploadFile) Readdir(int) ([]os.FileInfo, error) {
	return nil, &fs.PathError{Op: "readdir", Path: f.name, Err: fs.ErrInvalid}
}

// Close finalizes the upload. webdav calls it after copying the body and before
// writing the response, so this is where the pipeline's error becomes the HTTP
// error.
func (f *uploadFile) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return f.result
	}
	f.closed = true
	if spoolRequired(f.size) {
		f.result = f.finishSpool()
	} else {
		f.result = f.finishStream()
	}
	f.releaseSpool()
	if f.result != nil {
		f.fs.failures.record(f.result)
	}
	return f.result
}

func (f *uploadFile) writeSpool(p []byte) (int, error) {
	if f.spool == nil {
		file, err := os.CreateTemp(f.fs.tempDir, "teldrive-webdav-*.part")
		if err != nil {
			return 0, fmt.Errorf("create webdav upload spool: %w", err)
		}
		f.spool = file
	}
	written, err := f.spool.Write(p)
	f.spooled += int64(written)
	f.written = f.spooled
	return written, err
}

func (f *uploadFile) startStream() {
	if f.started {
		return
	}
	f.started = true
	reader, writer := io.Pipe()
	f.pipe = writer
	f.done = make(chan error, 1)
	go func() {
		err := f.fs.upload(f.ctx, f.parentID, f.name, reader, f.size)
		if err != nil {
			// Unblock the writer when the pipeline stops reading: without this a
			// failed part would leave the PUT handler parked in Write forever.
			_ = reader.CloseWithError(err)
		}
		f.done <- err
	}()
}

func (f *uploadFile) finishStream() error {
	if !f.started {
		// A zero-byte PUT still publishes an empty file.
		f.startStream()
	}
	if err := f.bodyError(); err != nil {
		_ = f.pipe.CloseWithError(err)
		<-f.done
		return err
	}
	if f.size >= 0 && f.written != f.size {
		err := fmt.Errorf("%w: received %d of %d bytes", ErrIncompleteBody, f.written, f.size)
		_ = f.pipe.CloseWithError(err)
		<-f.done
		return err
	}
	_ = f.pipe.Close()
	return <-f.done
}

func (f *uploadFile) finishSpool() error {
	if err := f.bodyError(); err != nil {
		return err
	}
	if f.spool == nil {
		return f.fs.upload(f.ctx, f.parentID, f.name, strings.NewReader(""), 0)
	}
	if _, err := f.spool.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind webdav upload spool: %w", err)
	}
	return f.fs.upload(f.ctx, f.parentID, f.name, io.NewSectionReader(f.spool, 0, f.spooled), f.spooled)
}

func (f *uploadFile) bodyError() error {
	if f.fs.meta.body.Clean() {
		return nil
	}
	return ErrIncompleteBody
}

func (f *uploadFile) releaseSpool() {
	if f.spool == nil {
		return
	}
	name := f.spool.Name()
	_ = f.spool.Close()
	_ = os.Remove(name)
	f.spool = nil
}

var _ xwebdav.File = (*uploadFile)(nil)
