package webdav

import (
	"errors"
	"path"
	"strings"
)

var (
	// ErrRootOperation rejects DAV operations that would modify the drive root
	// itself. The root is a virtual collection that maps to "no parent".
	ErrRootOperation = errors.New("webdav: the drive root cannot be modified")
	// ErrInvalidPath rejects DAV paths that cannot map onto a catalog path.
	ErrInvalidPath = errors.New("webdav: invalid path")
	// ErrUnknownSize rejects uploads whose final size never became known.
	ErrUnknownSize = errors.New("webdav: upload size is unknown")
	// ErrIncompleteBody reports a request body that ended before the client
	// declared it would. Publishing such a body would silently truncate a file.
	ErrIncompleteBody = errors.New("webdav: request body ended before it was complete")
	// ErrDisabled is answered when the WebDAV endpoint is switched off.
	ErrDisabled = errors.New("webdav: endpoint is disabled")
)

// normalizePath canonicalizes a DAV request path into the slash-separated form
// used by the catalog: the drive root is "/", children are "/a/b". Request paths
// arrive from webdav.Handler with the handler prefix already stripped, so they
// may be empty (the root) or start with a slash.
//
// Windows separators and NUL are rejected rather than translated, mirroring
// catalog.ResolveFolderPath: a backslash is a legal file name character on
// Telegram, so silently turning it into a separator would address the wrong
// entry.
func normalizePath(raw string) (string, error) {
	if strings.ContainsAny(raw, "\\\x00") {
		return "", ErrInvalidPath
	}
	for segment := range strings.SplitSeq(raw, "/") {
		if segment == ".." {
			return "", ErrInvalidPath
		}
	}
	cleaned := path.Clean("/" + raw)
	if cleaned == "/" {
		return "/", nil
	}
	return cleaned, nil
}

// splitParent splits a canonical non-root path into its parent collection path
// ("/" for the drive root) and its final element name.
func splitParent(canonical string) (parent, name string, err error) {
	if canonical == "/" || canonical == "" {
		return "", "", ErrRootOperation
	}
	index := strings.LastIndex(canonical, "/")
	parent = canonical[:index]
	if parent == "" {
		parent = "/"
	}
	return parent, canonical[index+1:], nil
}

// joinElement builds the canonical path of name inside parent.
func joinElement(parent, name string) string {
	if parent == "/" || parent == "" {
		return "/" + name
	}
	return parent + "/" + name
}
