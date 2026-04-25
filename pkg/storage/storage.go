// Package storage is goforge's blob abstraction. The Storage
// interface looks the same whether you're hitting AWS S3, Cloudflare
// R2, MinIO, Backblaze B2 or DigitalOcean Spaces — they all speak
// the S3 protocol and the framework supports them with one provider.
//
// Why one interface, not three packages? Because most apps only ever
// upload and download a few KB of metadata + tens-of-MB blobs, and
// they shouldn't have to fight a sprawling SDK every time. The
// interface here is small (Put, Get, Delete, Presign, List) and
// every method is contextual + cancelable.
//
// Authoring a presigned URL needs the user to be logged in and is
// what 90% of apps actually want for "user uploads". The framework
// returns the URL; the browser PUTs the bytes directly to object
// storage; your API never sees the file.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is the canonical "no such object" error every
// implementation maps onto. Callers compare with errors.Is so the
// underlying SDK type never leaks.
var ErrNotFound = errors.New("storage: object not found")

// Storage is the contract every backend satisfies.
type Storage interface {
	// Put writes (or overwrites) an object. ctype is forwarded to
	// Content-Type. size is required when the source is a stream
	// that doesn't implement Stat (e.g. multipart form upload);
	// pass -1 to defer to the SDK's auto-detection (which buffers).
	Put(ctx context.Context, key string, body io.Reader, size int64, ctype string) error

	// Get returns the object body. The caller MUST close the
	// returned reader.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes an object. Missing objects are not an error
	// (idempotent delete).
	Delete(ctx context.Context, key string) error

	// PresignPut returns a URL the caller can PUT to within the
	// supplied TTL. Used for direct browser uploads to bypass the
	// API server.
	PresignPut(ctx context.Context, key string, ttl time.Duration, ctype string) (string, error)

	// PresignGet returns a URL anyone with the link can GET within
	// the TTL. Use for short-lived "download this file" links.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)

	// List enumerates keys with the given prefix, up to limit.
	List(ctx context.Context, prefix string, limit int) ([]string, error)
}
