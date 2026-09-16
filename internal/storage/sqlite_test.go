package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestPublicationIsAtomicAndReprovesObjects(t *testing.T) {
	ctx := context.Background()
	cas, store := newStores(t)
	one := []byte("one")
	two := []byte("two")
	objectOne := objectFor(one)
	objectTwo := objectFor(two)
	if err := cas.Put(ctx, objectOne, strings.NewReader(string(one))); err != nil {
		t.Fatal(err)
	}
	publication := publicationFor("rev-atomic", "profile", 1, []Object{objectOne, objectTwo})

	if err := store.PublishRevision(ctx, publication); !errors.Is(err, ErrObjectMissing) {
		t.Fatalf("PublishRevision missing object = %v, want ErrObjectMissing", err)
	}
	if _, err := store.Revision(ctx, publication.RevisionID); !errors.Is(err, ErrRevisionNotFound) {
		t.Fatalf("failed publication revision visible: %v", err)
	}
	published, err := store.IsObjectPublished(ctx, objectOne.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if published {
		t.Fatal("object metadata from failed transaction is visible")
	}

	if err := cas.Put(ctx, objectTwo, strings.NewReader(string(two))); err != nil {
		t.Fatal(err)
	}
	path, err := cas.objectPath(objectTwo.SHA256, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishRevision(ctx, publication); !errors.Is(err, ErrCorruptObject) {
		t.Fatalf("PublishRevision corrupt object = %v, want ErrCorruptObject", err)
	}
	if _, err := store.Revision(ctx, publication.RevisionID); !errors.Is(err, ErrRevisionNotFound) {
		t.Fatalf("corrupt publication became visible: %v", err)
	}
}

func TestPublicationRecoveryIdempotencyAndImmutability(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cas, err := OpenCAS(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("durable")
	object := objectFor(body)
	if err := cas.Put(ctx, object, strings.NewReader(string(body))); err != nil {
		t.Fatal(err)
	}
	dbPath := root + "/metadata.sqlite3"
	store, err := OpenSQLite(dbPath, cas)
	if err != nil {
		t.Fatal(err)
	}
	publication := publicationFor("rev-durable", "profile", 7, []Object{object})
	if err := store.PublishRevision(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if err := store.PublishRevision(ctx, publication); err != nil {
		t.Fatalf("idempotent publication: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE revisions SET sequence = 8 WHERE id = 'rev-durable'`); err == nil {
		t.Fatal("immutable revision UPDATE unexpectedly succeeded")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopenedCAS, err := OpenCAS(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLite(dbPath, reopenedCAS)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := reopened.Revision(ctx, publication.RevisionID)
	if err != nil {
		t.Fatalf("revision missing after reopen: %v", err)
	}
	if stored.ManifestSHA256 != publication.Manifest.SHA256 || string(stored.Manifest) != string(publication.Manifest.Bytes) {
		t.Fatalf("recovered manifest mismatch: %#v", stored)
	}
	published, err := reopened.IsObjectPublished(ctx, object.SHA256)
	if err != nil || !published {
		t.Fatalf("published object after reopen = %v, %v", published, err)
	}

	changed := publication
	changed.Manifest = manifestFor([]byte(`{"changed":true}`))
	if err := reopened.PublishRevision(ctx, changed); !errors.Is(err, ErrImmutableConflict) {
		t.Fatalf("changed immutable revision error = %v, want ErrImmutableConflict", err)
	}
}

func TestConcurrentPublicationsCannotPartiallyWin(t *testing.T) {
	ctx := context.Background()
	cas, store := newStores(t)
	bodyA := []byte("a")
	bodyB := []byte("b")
	objectA := objectFor(bodyA)
	objectB := objectFor(bodyB)
	if err := cas.Put(ctx, objectA, strings.NewReader(string(bodyA))); err != nil {
		t.Fatal(err)
	}
	if err := cas.Put(ctx, objectB, strings.NewReader(string(bodyB))); err != nil {
		t.Fatal(err)
	}
	publications := []RevisionPublication{
		publicationFor("rev-a", "profile", 11, []Object{objectA}),
		publicationFor("rev-b", "profile", 11, []Object{objectB}),
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, publication := range publications {
		publication := publication
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- store.PublishRevision(ctx, publication)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var success, conflict int
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrImmutableConflict):
			conflict++
		default:
			t.Fatalf("unexpected publication result: %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d, want 1/1", success, conflict)
	}
	for _, publication := range publications {
		_, err := store.Revision(ctx, publication.RevisionID)
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrRevisionNotFound) {
			t.Fatal(err)
		}
		published, err := store.IsObjectPublished(ctx, publication.ExpectedObjects[0].SHA256)
		if err != nil {
			t.Fatal(err)
		}
		if published {
			t.Fatal("losing concurrent publication leaked object metadata")
		}
	}
}

func TestPublicationRejectsManifestDigestMismatch(t *testing.T) {
	_, store := newStores(t)
	publication := publicationFor("rev", "profile", 1, nil)
	publication.Manifest.SHA256 = strings.Repeat("0", 64)
	if err := store.PublishRevision(context.Background(), publication); !errors.Is(err, ErrInvalidPublication) {
		t.Fatalf("manifest mismatch error = %v, want ErrInvalidPublication", err)
	}
}

func newStores(t *testing.T) (*CAS, *SQLiteStore) {
	t.Helper()
	root := t.TempDir()
	cas, err := OpenCAS(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenSQLite(root+"/metadata.sqlite3", cas)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return cas, store
}

func publicationFor(id, profile string, sequence int64, objects []Object) RevisionPublication {
	manifest := []byte(`{"already_validated":true,"id":"` + id + `"}`)
	return RevisionPublication{
		RevisionID:      id,
		ProfileID:       profile,
		Sequence:        sequence,
		Manifest:        manifestFor(manifest),
		ExpectedObjects: objects,
	}
}

func manifestFor(body []byte) ValidatedManifest {
	sum := sha256.Sum256(body)
	return ValidatedManifest{Bytes: body, SHA256: hex.EncodeToString(sum[:])}
}
