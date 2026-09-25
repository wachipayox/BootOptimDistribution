# Local CAS and SQLite storage

This package is the storage cut of the Pandora PR #35 distribution design. It
accepts release material only after the validation/signing layer has decided it
is valid. It does not parse manifests, resolve inheritance, verify Ed25519,
apply anti-rollback policy, authorize HTTP calls, or touch player files.

## HTTP-facing storage API

The HTTP/admin layer should depend on the two small interfaces in
`internal/storage`:

```go
type ObjectIngester interface {
    Put(context.Context, Object, io.Reader) error
    Verify(context.Context, Object) error
}

type RevisionPublisher interface {
    PublishRevision(context.Context, RevisionPublication) error
}
```

`Object` is exactly `{SHA256, Size}`. `RevisionPublication` carries revision and
profile ids, sequence, canonical manifest bytes already validated by the
release layer, their already-computed SHA-256, and the exact expected object
set. Storage deliberately has no manifest parser dependency. As a defensive
boundary it re-hashes the supplied manifest bytes and refuses a mismatched
manifest digest; that is not canonicalization or signature verification.

Object uploads never trust the requested hash as proof. `CAS.Put` hashes while
streaming into a random `incoming/*.part`, requires the exact size and digest,
fsyncs the file, then atomically renames it into the digest-derived CAS path and
fsyncs both directories. Existing matching content is an idempotent success;
an existing wrong file is reported as corruption and is never overwritten by
the normal existing-object path. Lowercase 64-hex validation happens before
any path is constructed.

`SQLiteStore.PublishRevision` physically re-hashes every referenced CAS object
before opening its metadata transaction. The transaction records object rows,
the immutable revision row and all revision/object edges together. A revision
therefore becomes query-visible only after all expected objects have passed the
physical check and the transaction commits. Schema triggers reject update or
delete of published revisions, their object edges, and published object rows.
There is no revision/object GC in v1.

## Default layout and permissions

The deployment default remains the PR #35 layout:

```text
/etc/bootoptim-distribution/
  config.toml
  service.env                  # optional runtime secret, never a signing key
  trusted-release-keys/         # public material only

/var/lib/bootoptim-distribution/
  metadata.sqlite3
  metadata.sqlite3-wal
  objects/sha256/aa/<62 hex>
  incoming/<random>.part
```

Use `/var/lib/bootoptim-distribution` as the `OpenCAS` root and
`/var/lib/bootoptim-distribution/metadata.sqlite3` as the SQLite path. The
service account should exclusively own the state directory, with directory mode
`0700` and process umask `0077`; the package creates its state directories as
`0700`, the database as `0600`, incoming files as `0600`, and committed CAS
objects as `0444`. Configuration under `/etc/bootoptim-distribution` should be
root/service-readable only; `service.env`, when used, is `0600`. No systemd unit
is added by this change.

`OpenCAS` rejects symlinks for the service-owned root/subdirectories it opens.
It performs no startup scan of `incoming`: an interrupted temporary remains an
inaccessible orphan until a future explicit, scoped GC policy handles it.

The default object upload ceiling is 2 GiB (`DefaultMaxObjectBytes`) and callers
may configure a smaller positive limit.

## SQLite durability and concurrency boundary

SQLite is configured with foreign keys, WAL, `synchronous=FULL`, and a five
second busy timeout. This v1 store intentionally uses one database connection
and a process-local publication mutex. Concurrent publication goroutines in one
service process are safe and unique `(profile_id, sequence)` constraints make a
racing loser atomic: it leaves no revision/object metadata behind.

The mutex is not a distributed lock. Running multiple service writer processes
against the same state directory is outside the tested v1 contract, even though
SQLite itself still enforces database constraints. Deploy exactly one writer
process per state directory. There is no multi-process filesystem/SQLite commit
protocol and no GC process in this cut.

## Dependency and test scope

New direct dependency: `modernc.org/sqlite v1.20.4`, a CGo-free SQLite driver,
licensed under a BSD-style/BSD-3-Clause license; the bundled upstream SQLite
code is public domain. Transitive modules are those declared by that release.
No cryptography/signing library is added here.

Focused tests cover correct/wrong hashes, short writes, idempotent upload,
interrupted temporaries, malicious digest/path input, symlinked state paths,
existing CAS corruption, publication object re-checks, transaction visibility,
revision immutability/idempotence, reopen recovery, manifest-digest mismatch and
same-sequence concurrent publication. Missing coverage is deliberate fault
injection for kernel/fsync/ENOSPC/power-loss behavior, hostile concurrent state
mutation by another process, multi-process writers, and any future GC. Those
need dedicated integration/fault tests rather than simulated success claims.
