package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCASPutVerifyAndIdempotent(t *testing.T) {
	root := t.TempDir()
	cas, err := OpenCAS(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("verified object bytes")
	expected := objectFor(body)

	if err := cas.Put(context.Background(), expected, strings.NewReader(string(body))); err != nil {
		t.Fatalf("first Put: %v", err)
	}
	if err := cas.Verify(context.Background(), expected); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if err := cas.Put(context.Background(), expected, strings.NewReader(string(body))); err != nil {
		t.Fatalf("idempotent Put: %v", err)
	}

	path, err := cas.objectPath(expected.SHA256, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0444 {
		t.Fatalf("object mode = %o, want 0444", got)
	}
}

func TestCASRejectsWrongHashAndTruncatedWrite(t *testing.T) {
	cas, err := OpenCAS(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("complete")
	wrong := objectFor([]byte("different"))
	wrong.Size = int64(len(body))
	if err := cas.Put(context.Background(), wrong, strings.NewReader(string(body))); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("wrong hash error = %v, want ErrDigestMismatch", err)
	}

	expected := objectFor(body)
	if err := cas.Put(context.Background(), expected, strings.NewReader("short")); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("truncated error = %v, want ErrSizeMismatch", err)
	}
	if err := cas.Verify(context.Background(), expected); !errors.Is(err, ErrObjectMissing) {
		t.Fatalf("failed upload became visible: %v", err)
	}
}

func TestCASInterruptedTempIsNeverAnObject(t *testing.T) {
	root := t.TempDir()
	cas, err := OpenCAS(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("eventual object")
	expected := objectFor(body)
	part := filepath.Join(cas.incomingDir, "interrupted.part")
	if err := os.WriteFile(part, body[:4], 0600); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenCAS(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Verify(context.Background(), expected); !errors.Is(err, ErrObjectMissing) {
		t.Fatalf("interrupted temp visible as CAS object: %v", err)
	}
	if _, err := os.Stat(part); err != nil {
		t.Fatalf("OpenCAS must not scan/delete incoming temps: %v", err)
	}
	if err := reopened.Put(context.Background(), expected, strings.NewReader(string(body))); err != nil {
		t.Fatalf("Put after interrupted temp: %v", err)
	}
}

func TestCASRejectsMaliciousDigestsAndStateSymlink(t *testing.T) {
	cas, err := OpenCAS(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{
		"../" + strings.Repeat("a", 61),
		strings.Repeat("A", 64),
		strings.Repeat("a", 63) + "/",
		strings.Repeat("a", 65),
	}
	for _, digest := range bad {
		err := cas.Put(context.Background(), Object{SHA256: digest, Size: 0}, strings.NewReader(""))
		if !errors.Is(err, ErrInvalidDigest) {
			t.Errorf("digest %q error = %v, want ErrInvalidDigest", digest, err)
		}
	}

	root := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, "objects")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := OpenCAS(root, 1024); err == nil {
		t.Fatal("OpenCAS accepted symlinked state directory")
	}
}

func TestCASDetectsExistingCorruptionInsteadOfOverwriting(t *testing.T) {
	cas, err := OpenCAS(t.TempDir(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("good")
	expected := objectFor(body)
	path, err := cas.objectPath(expected.SHA256, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("evil"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := cas.Put(context.Background(), expected, strings.NewReader(string(body))); !errors.Is(err, ErrCorruptObject) {
		t.Fatalf("corrupt existing object error = %v, want ErrCorruptObject", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "evil" {
		t.Fatalf("corrupt existing object was overwritten: %q", got)
	}
}

func objectFor(body []byte) Object {
	sum := sha256.Sum256(body)
	return Object{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}
}
