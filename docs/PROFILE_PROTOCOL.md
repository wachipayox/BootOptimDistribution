# Private profile protocol contract (draft v1)

This is the shared boundary for Distribution admin/publishing and Pandora
profile discovery/update. It is the product contract for the first service
implementation; changes must update both this document and the Pandora
integration plan. The profile API and panel composition are in review and have
not yet landed on `agent/integration-current`.

## Authorities and identifiers

- Distribution is authoritative for global profile IDs, immutable revision
  envelopes, public signing keys, and global content objects.
- Pandora is authoritative for local profiles, local parent references,
  user-owned files, applied-revision state, and local recovery data.
- A global revision may pin one exact parent tuple
  `(profile_id, revision_id, manifest_sha256)`. Never interpret a parent as
  `latest`. Resolve and verify the full chain up to the configured depth.
- A local profile stores its local parent pin and overlay privately. It never
  uploads its overlay or local instance file listing.

## Signed revision and object boundary

Use the canonical signed envelope and manifest model from
`internal/revision/types.go`. Canonicalization is RFC 8785 JCS; signatures are
Ed25519 over canonical manifest bytes. Hashes are lowercase SHA-256 hex and
object paths are validated relative paths. Manifest/revision identity is
immutable. The service verifies the envelope and every referenced object's
size/hash before publication becomes visible.

Policy semantics to preserve in the client-facing manifest:

- `enforced`: install the published value on update. If the old local value
  differs, first retain it in per-profile recoverable conflict storage.
- `default_once`: seed only on first installation when no value exists; then
  the path is user-owned.
- `user_owned`: distribute a first seed only when absent and preserve local
  edits thereafter.
- A supported structured config may additionally identify selected options
  (format + stable selector + desired value); all unspecified options remain
  local. Reject unsupported/ambiguous selectors at publication, never perform
  broad format-agnostic text replacement.

These are target semantics, not all current schema capabilities. The current
validator accepts only `enforced` and `default_once`; the explicit `user_owned`
policy and option-level config selectors remain unimplemented and must not be
claimed as publishable until the Distribution and Pandora schemas agree.

## HTTP shape to implement

All state-changing admin routes require authenticated administrator session and
CSRF protection over Distribution-native HTTPS. No Caddy dependency. Release
signing remains separate: browser and service may see an unsigned manifest and
the resulting signature, but never the Ed25519 private key. A local signer
tool signs a saved canonical request and returns an envelope.

Public/client read routes:

```text
GET /v1/profiles
GET /v1/profiles/{profile_id}/revisions/{revision_id}
GET /v1/objects/sha256/{sha256}
```

Administrator routes:

```text
GET  /v1/admin/profiles
GET  /v1/admin/profiles/{profile_id}/revisions
POST /v1/admin/objects/sha256/{sha256}                 # authenticated staged upload
POST /v1/admin/revisions                              # signed immutable envelope
POST /v1/admin/profiles/{profile_id}/channels/{name}/promote
POST /v1/admin/profiles/{profile_id}/channels/{name}/rollback
```

Profile creation is the publication of a valid root revision whose profile ID
does not yet exist. Child publication pins its parent in the manifest. Uploads
that have not yet been referenced are not visible as a profile/revision and
must be eligible for later orphan collection. Promotion/rollback changes only
channel state; it never mutates the published revision.

Presentation-only read-model routes may include:

```text
GET /v1/admin/ui/overview
GET /v1/admin/ui/profiles/{profile_id}/revisions
GET /v1/admin/ui/revisions/{revision_id}
```

The embedded overview remains at `/admin/api/overview`; profile history uses
the authenticated `/v1/admin/profiles/{profile_id}/revisions` API route.
Responses use JSON, explicit protocol/schema versions, bounded payloads, and
stable error codes. Unauthorized objects/profiles should not disclose existence
where that distinction leaks private state.

## Browser publication flow

1. Admin signs into the HTTPS panel and chooses a source folder using a
   browser directory picker. It is used only to read the selected pack files.
2. The panel hashes files, compares the folder with the selected parent
   revision's effective manifest, and previews additions, replacements,
   removals, size, policies, and unsupported paths.
3. The panel uploads changed bytes to authenticated staging endpoints and
   downloads a canonical signing request. It never accesses a private key.
4. A separate local signer must display profile/parent identity and digest,
   validate the request shape, then sign it. The admin returns the signed
   envelope to the panel for atomic publication. The signer tool is not yet
   included in the current implementation.
5. The service verifies authorization, signature, parent pin, object hashes,
   policies, sequence and anti-rollback invariants before making the revision
   visible.

The initial acceptance fixture is a synthetic root and child profile. No
player save/config directory should be used for the first test.
