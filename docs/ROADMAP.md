# Distribution service roadmap

`agent/integration-current` is the service integration authority; `main` alone
is deployable. The current integrated build (`e89e325`, 2026-09-26) provides a
direct-LAN, read-only admin page and the initial service/version endpoints. It
does not yet publish or distribute profiles. The running panel observed at
`192.168.1.69:8088/admin/` is therefore an operational shell, not a profile
manager.

The in-review Distribution vertical slice now adds native HTTPS/admin sessions,
CSRF-protected signed profile publication, LAN-restricted launcher reads, and
the first browser folder-to-revision workflow. It is not integrated until its
PR passes Linux CI. The local signer utility, trusted-key bootstrap UX, channel
controls in the panel, structured config selectors, and Pandora's end-to-end
install/update/repair flow remain open work. Current schema policies are only
`enforced` and `default_once`; do not treat the planned `user_owned` and
option-selector semantics below as implemented.

The product is a private profile distribution service, not a public modpack
catalog. The Linux service owns immutable global profiles/revisions and their
content objects. Pandora owns local profiles and the user's running game
directories. The service never inventories a player's `.minecraft` and never
participates in the launch path.

## Decisions already made

- Keep the service reachable on the private LAN without Caddy. Implement
  Distribution-native HTTPS and administrator login before enabling mutations.
- Global revisions are immutable, signed, content-addressed, and may pin a
  parent profile revision. Clients can derive local profiles from a global
  profile without uploading those private changes.
- Store only signing public keys on the server. A separate local signer holds
  the private release key; neither the browser nor the service receives it.
- The first end-to-end publication uses a small synthetic pack, not real player
  data. The browser selects a source folder and previews the proposed changes.
- A revision records its parent pin and its own changes/removals. Profile
  history, not a full client disk scan, defines which files an update needs to
  apply.
- Keep the default service listener loopback until a deliberate LAN
  configuration enables the authenticated HTTPS endpoint.

## Delivery sequence

### 1. Authenticated LAN administration

Status: implemented in the in-review vertical slice; awaiting CI and merge.

Replace the current read-only badge/page with a useful admin shell and clear
navigation for Overview, Global profiles, and Service settings. The overview
prioritizes profiles, current revisions, publication/update activity, and
actionable errors. Move commit, schema, raw storage counts, and similar
diagnostics to a secondary service-details view.

Add Distribution-native HTTPS and administrator login/session protection for
all mutation routes. Keep a documented private-LAN bootstrap path, secure
session cookies, CSRF protection, and explicit bind-address configuration.
Do not add Caddy as a dependency and do not expose unauthenticated write APIs.

### 2. Global profile publication and test profile creation

Status: publication API and initial browser workflow are in review. The local
signer and a complete synthetic-pack run remain required for acceptance.

Implement the service API and browser workflow to create a global profile,
select a local folder, inspect additions/changes/removals, and publish a new
immutable revision. A separate local signer signs the canonical revision
payload; the admin browser stages a publication request and completes it with
the signature without ever handling the private key. The server verifies the
signature and object hashes before atomically making the revision visible.

Use the existing SQLite/CAS and signed-revision design. Preserve immutable
history, pinned parent references, anti-rollback checks, path validation,
deduplication, and authorization checks. A failed publication must leave no
partially visible revision. Start with a small synthetic profile that exercises
one mod, one config, one resource pack, and one removal.

### 3. Branch graph and effective profile resolution

Status: planned. Pandora's local ownership/reconciliation backend is in a
separate in-review PR; server/client schema agreement and end-to-end checks are
still outstanding.

Allow multiple child profiles per parent, with one pinned parent revision per
child revision. Support global-to-global and global-to-local derivation on the
client; local-to-local derivation remains client-local. Resolution reports the
effective inherited tree plus each entry's source and policy without exposing
private local overlays to the server.

Represent add, replace, remove, and policy changes in revision history. A
child can replace/remove an inherited mod or other path. Configuration policies
must distinguish enforced values, seed-once defaults, and explicit option
selectors for supported config formats. Reject ambiguous selectors at
publication time. Avoid format-agnostic text replacement.

### 4. Client protocol and operational hardening

Implement public-profile discovery, immutable revision resolution, and
authenticated object download endpoints required by Pandora. Publish a protocol
version and capability set so client/server compatibility is explicit. Add
backup/restore, retention, audit-safe publication diagnostics, and a
loopback/LAN deployment smoke path. The service still stores only global
profile data and never receives a client filesystem listing.

## Acceptance path

1. Sign in from another device on the private LAN over HTTPS.
2. Create a synthetic global root profile by choosing a folder and review the
   file-level diff.
3. Sign and publish it with the local signer; confirm the server rejects a
   malformed signature or hash and leaves no partial revision.
4. Create a child revision that changes a mod, sets a selected config option,
   removes one inherited file, and adds a resource pack; verify the parent
   remains unchanged and the resolved history is reproducible.
5. Have Pandora install and update a derived test profile using only the
   changed revision entries. Confirm a no-op update does not walk/hash the
   entire `.minecraft` tree. Exercise explicit full repair separately.

Menu and service diagnostics must make clear that the test profile is synthetic
and distinguish publication, download, reconciliation, and repair operations.
