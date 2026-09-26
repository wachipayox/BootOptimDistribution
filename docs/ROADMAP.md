# Distribution service roadmap

`agent/integration-current` is the service integration authority; `main` alone
is deployable. The current integration (`4c164b4`, 2026-09-26) contains the
authenticated native-HTTPS admin panel, signed profile API, LAN-restricted
launcher reads, and browser folder-to-revision publication workflow. These
features are not yet in `main`, so the service currently installed on the
server does not provide them. The local signer utility and a full synthetic
publication/client run remain required before the first release. The panel
also lets the operator point a profile's `stable` channel at a published
revision from its history.

Current schema policies are only `enforced` and `default_once`; do not treat
the planned `user_owned` and option-selector semantics as implemented. The
launcher has profile discovery/install/update foundations on its own
integration branch, but it still needs a simple HTTPS/protocol connection
check and end-to-end validation against a published synthetic profile.

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

Status: implemented on `agent/integration-current`; not yet released from
`main`.

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

Status: publication API and initial browser workflow are integrated. The local
signer and a complete synthetic-pack run remain required for acceptance.

The service API and browser workflow create a global profile, select a local
folder, inspect additions/changes/removals, and stage a new immutable revision.
Profile history also supports promoting a revision to `stable`, which the
overview displays. Next, provide a separate local signer for the canonical
revision request; the admin browser must never handle the private key. Then
validate publication with a synthetic pack and verify the service rejects
invalid signatures/hashes without exposing a partial revision.

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

Public-profile discovery, immutable revision resolution, authenticated object
downloads, protocol version, and capability reporting are integrated. Next,
validate client connection/discovery against the LAN HTTPS deployment, then add
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
