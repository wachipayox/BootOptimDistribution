package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const DefaultMaxObjectBytes int64 = 2 << 30

var (
	ErrInvalidDigest  = errors.New("invalid sha256 digest")
	ErrSizeMismatch   = errors.New("object size mismatch")
	ErrDigestMismatch = errors.New("object sha256 mismatch")
	ErrObjectMissing  = errors.New("object missing")
	ErrCorruptObject  = errors.New("object corrupt")
)

type Object struct {
	SHA256 string
	Size   int64
}

type ObjectIngester interface {
	Put(context.Context, Object, io.Reader) error
	Verify(context.Context, Object) error
}

type CAS struct {
	root           string
	objectsDir     string
	incomingDir    string
	maxObjectBytes int64
}

func OpenCAS(root string, maxObjectBytes int64) (*CAS, error) {
	if root == "" {
		return nil, errors.New("storage root is required")
	}
	if maxObjectBytes <= 0 {
		maxObjectBytes = DefaultMaxObjectBytes
	}
	cleanRoot := filepath.Clean(root)
	if err := os.MkdirAll(cleanRoot, 0700); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	if err := requireDirectoryNoSymlink(cleanRoot); err != nil {
		return nil, err
	}
	objects, err := ensureChildDirectory(cleanRoot, "objects")
	if err != nil {
		return nil, err
	}
	shaDir, err := ensureChildDirectory(objects, "sha256")
	if err != nil {
		return nil, err
	}
	incoming, err := ensureChildDirectory(cleanRoot, "incoming")
	if err != nil {
		return nil, err
	}
	return &CAS{
		root:           cleanRoot,
		objectsDir:     shaDir,
		incomingDir:    incoming,
		maxObjectBytes: maxObjectBytes,
	}, nil
}

func (c *CAS) Put(ctx context.Context, expected Object, src io.Reader) error {
	if err := validateObject(expected); err != nil {
		return err
	}
	if expected.Size > c.maxObjectBytes {
		return fmt.Errorf("%w: %d exceeds configured maximum %d", ErrSizeMismatch, expected.Size, c.maxObjectBytes)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	finalPath, err := c.objectPath(expected.SHA256, true)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(finalPath); err == nil {
		if err := c.verifyPath(ctx, finalPath, expected); err != nil {
			return fmt.Errorf("%w: existing object %s: %v", ErrCorruptObject, expected.SHA256, err)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect existing object: %w", err)
	}

	tmp, err := os.CreateTemp(c.incomingDir, ".upload-*.part")
	if err != nil {
		return fmt.Errorf("create incoming object: %w", err)
	}
	tmpName := tmp.Name()
	published := false
	defer func() {
		_ = tmp.Close()
		if !published {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0600); err != nil {
		return fmt.Errorf("restrict incoming object: %w", err)
	}

	h := sha256.New()
	written, err := copyWithContext(ctx, io.MultiWriter(tmp, h), io.LimitReader(src, expected.Size+1))
	if err != nil {
		return fmt.Errorf("write incoming object: %w", err)
	}
	if written != expected.Size {
		return fmt.Errorf("%w: got %d, want %d", ErrSizeMismatch, written, expected.Size)
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expected.SHA256 {
		return fmt.Errorf("%w: got %s, want %s", ErrDigestMismatch, actual, expected.SHA256)
	}
	if err := tmp.Chmod(0444); err != nil {
		return fmt.Errorf("make object immutable: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("fsync incoming object: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close incoming object: %w", err)
	}

	// Rename is same-filesystem because incoming and objects live below root.
	// A same-digest concurrent writer can only replace these already-verified
	// bytes with another stream that passed the same digest+size checks.
	if err := os.Rename(tmpName, finalPath); err != nil {
		return fmt.Errorf("publish object: %w", err)
	}
	published = true
	if err := syncDirectory(filepath.Dir(finalPath)); err != nil {
		return fmt.Errorf("fsync object directory: %w", err)
	}
	if err := syncDirectory(c.incomingDir); err != nil {
		return fmt.Errorf("fsync incoming directory: %w", err)
	}
	return nil
}

func (c *CAS) Verify(ctx context.Context, expected Object) error {
	if err := validateObject(expected); err != nil {
		return err
	}
	path, err := c.objectPath(expected.SHA256, false)
	if err != nil {
		return err
	}
	if err := c.verifyPath(ctx, path, expected); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrObjectMissing, expected.SHA256)
		}
		return err
	}
	return nil
}

func (c *CAS) verifyPath(ctx context.Context, path string, expected Object) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: non-regular CAS entry", ErrCorruptObject)
	}
	if info.Size() != expected.Size {
		return fmt.Errorf("%w: stored size %d, want %d", ErrSizeMismatch, info.Size(), expected.Size)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := copyWithContext(ctx, h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expected.SHA256 {
		return fmt.Errorf("%w: stored hash %s, want %s", ErrDigestMismatch, actual, expected.SHA256)
	}
	return nil
}

func (c *CAS) objectPath(digest string, createBucket bool) (string, error) {
	if !validDigest(digest) {
		return "", ErrInvalidDigest
	}
	bucket := filepath.Join(c.objectsDir, digest[:2])
	if createBucket {
		var err error
		bucket, err = ensureChildDirectory(c.objectsDir, digest[:2])
		if err != nil {
			return "", err
		}
	} else if err := requireDirectoryNoSymlink(bucket); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return filepath.Join(bucket, digest[2:]), nil
		}
		return "", err
	}
	return filepath.Join(bucket, digest[2:]), nil
}

func validateObject(object Object) error {
	if !validDigest(object.SHA256) {
		return ErrInvalidDigest
	}
	if object.Size < 0 {
		return fmt.Errorf("%w: negative expected size", ErrSizeMismatch)
	}
	return nil
}

func validDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for i := 0; i < len(digest); i++ {
		b := digest[i]
		if (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			return false
		}
	}
	return true
}

func ensureChildDirectory(parent, name string) (string, error) {
	path := filepath.Join(parent, name)
	created := false
	if err := os.Mkdir(path, 0700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create state directory %s: %w", path, err)
		}
	} else {
		created = true
	}
	if err := requireDirectoryNoSymlink(path); err != nil {
		return "", err
	}
	if created {
		if err := syncDirectory(parent); err != nil {
			return "", fmt.Errorf("fsync parent directory %s: %w", parent, err)
		}
	}
	return path, nil
}

func requireDirectoryNoSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("unsafe state path %s: directory required and symlinks forbidden", path)
	}
	return nil
}

func syncDirectory(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 128*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
	}
}
