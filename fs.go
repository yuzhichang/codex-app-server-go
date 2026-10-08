package codexgo

import (
	"context"
	"encoding/base64"

	"github.com/zealbase/codex-app-server-go/internal/protocol"
)

// Filesystem RPCs (fs/*).
//
// These operate on the *host* filesystem of the app-server, not the client's. Paths are
// `AbsolutePathBuf` (an alias for string): upstream requires an absolute, normalized path.
//
// Watches are connection-scoped upstream ("Connection-scoped watch identifier used for
// `fs/unwatch` and `fs/changed`"), so every watch dies with the connection. After a
// reconnect, re-issue FSWatch for each watch you still want.

// FSReadFile reads a file from the host. The payload arrives base64-encoded; call
// FSReadFileBytes when you want the decoded bytes.
func (c *Client) FSReadFile(ctx context.Context, req FsReadFileParams) (FsReadFileResponse, error) {
	var resp FsReadFileResponse
	if err := c.transport.Call(ctx, protocol.MethodFsReadFile, req, &resp); err != nil {
		return FsReadFileResponse{}, err
	}
	return resp, nil
}

// FSReadFileBytes is FSReadFile plus base64 decoding.
func (c *Client) FSReadFileBytes(ctx context.Context, path string) ([]byte, error) {
	resp, err := c.FSReadFile(ctx, FsReadFileParams{Path: AbsolutePathBuf(path)})
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(resp.DataBase64)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// FSWriteFile writes base64-encoded data to a host file. Use FSWriteFileBytes for raw bytes.
func (c *Client) FSWriteFile(ctx context.Context, req FsWriteFileParams) (FsWriteFileResponse, error) {
	var resp FsWriteFileResponse
	if err := c.transport.Call(ctx, protocol.MethodFsWriteFile, req, &resp); err != nil {
		return FsWriteFileResponse{}, err
	}
	return resp, nil
}

// FSWriteFileBytes is FSWriteFile plus base64 encoding.
func (c *Client) FSWriteFileBytes(ctx context.Context, path string, data []byte) error {
	_, err := c.FSWriteFile(ctx, FsWriteFileParams{
		Path:       AbsolutePathBuf(path),
		DataBase64: base64.StdEncoding.EncodeToString(data),
	})
	return err
}

// FSCreateDirectory creates a directory. Set Recursive for `mkdir -p` semantics.
func (c *Client) FSCreateDirectory(ctx context.Context, req FsCreateDirectoryParams) (FsCreateDirectoryResponse, error) {
	var resp FsCreateDirectoryResponse
	if err := c.transport.Call(ctx, protocol.MethodFsCreateDirectory, req, &resp); err != nil {
		return FsCreateDirectoryResponse{}, err
	}
	return resp, nil
}

// FSGetMetadata returns stat-like metadata for a host path.
func (c *Client) FSGetMetadata(ctx context.Context, req FsGetMetadataParams) (FsGetMetadataResponse, error) {
	var resp FsGetMetadataResponse
	if err := c.transport.Call(ctx, protocol.MethodFsGetMetadata, req, &resp); err != nil {
		return FsGetMetadataResponse{}, err
	}
	return resp, nil
}

// FSReadDirectory lists a directory's immediate entries.
func (c *Client) FSReadDirectory(ctx context.Context, req FsReadDirectoryParams) (FsReadDirectoryResponse, error) {
	var resp FsReadDirectoryResponse
	if err := c.transport.Call(ctx, protocol.MethodFsReadDirectory, req, &resp); err != nil {
		return FsReadDirectoryResponse{}, err
	}
	return resp, nil
}

// FSRemove removes a file or directory. Set Recursive for trees; Force ignores absence.
func (c *Client) FSRemove(ctx context.Context, req FsRemoveParams) (FsRemoveResponse, error) {
	var resp FsRemoveResponse
	if err := c.transport.Call(ctx, protocol.MethodFsRemove, req, &resp); err != nil {
		return FsRemoveResponse{}, err
	}
	return resp, nil
}

// FSCopy copies a file or directory tree. Set Recursive for directory copies.
func (c *Client) FSCopy(ctx context.Context, req FsCopyParams) (FsCopyResponse, error) {
	var resp FsCopyResponse
	if err := c.transport.Call(ctx, protocol.MethodFsCopy, req, &resp); err != nil {
		return FsCopyResponse{}, err
	}
	return resp, nil
}

// FSWatch starts watch notifications for a path.
//
// WatchID is yours to choose and is echoed back on every FsChangedEvent, so it is the
// correlation key for multiple concurrent watches. The server also returns the resolved
// Path. Remember that watches are connection-scoped: re-issue this after a reconnect.
func (c *Client) FSWatch(ctx context.Context, req FsWatchParams) (FsWatchResponse, error) {
	var resp FsWatchResponse
	if err := c.transport.Call(ctx, protocol.MethodFsWatch, req, &resp); err != nil {
		return FsWatchResponse{}, err
	}
	return resp, nil
}

// FSUnwatch stops a watch previously started with FSWatch.
func (c *Client) FSUnwatch(ctx context.Context, req FsUnwatchParams) (FsUnwatchResponse, error) {
	var resp FsUnwatchResponse
	if err := c.transport.Call(ctx, protocol.MethodFsUnwatch, req, &resp); err != nil {
		return FsUnwatchResponse{}, err
	}
	return resp, nil
}
