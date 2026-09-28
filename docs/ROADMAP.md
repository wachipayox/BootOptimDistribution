# Distribution service roadmap

`agent/integration-current` is the service integration authority; `main` alone
is deployable. As of 2026-09-28, deployable `main` and the live service are at
`ce2cf51` / Distribution `0.2.1`; `agent/integration-current` is at `d3fc360`.
The authenticated native-HTTPS admin panel, signed profile API, LAN-restricted
launcher reads, schema-v2 signed per-setting rule validation, refined `v16`
profile workflow, and official Minecraft/NeoForge version catalog are present
in the live build. The live version endpoint reports capability
`signed-config-setting-rules-v1`; the top-level protocol schema remains 1, while
individual manifests may use schema 2. The live profile catalog is currently
empty, so an end-to-end signed publication and client application have not yet
been demonstrated.

The offline signer and its recovery/rotation tests are merged into
`agent/integration-current` (PRs #27 and #28), but not yet into deployable
`main`. The signer runs on the administrator PC; it is not required on the
Linux service. Use a trusted checkout containing the signer while `main` has
not yet been promoted. The panel lets the operator point a profile's `stable`
channel at a published revision from its history.

File schema policies are `enforced` and `default_once`. Manifest schema v2
adds per-setting TOML, `.properties`, and `.txt` rules in the panel, API, and
server validator; that validator is already in the live `0.2.1` build. The
matching client transaction/merge is in Pandora PR #76. Its CI checks passed
as of this roadmap revision, but the PR remains a draft pending a synthetic
signed server publication and live client application. The broader
`user_owned` file policy and arbitrary structured formats remain out of scope.

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
- Preserve all trusted public keys during signer rotation so old revisions
  remain verifiable. A replacement PC creates its own new signer; Distribution
  must never generate or retain release private keys.
- The first end-to-end publication uses a small synthetic pack, not real player
  data. The browser selects a source folder and previews the proposed changes.
- A revision records its parent pin and its own changes/removals. Profile
  history, not a full client disk scan, defines which files an update needs to
  apply.
- Keep the default service listener loopback until a deliberate LAN
  configuration enables the authenticated HTTPS endpoint.

## Delivery sequence

### 1. Authenticated LAN administration

Status: implemented and deployed as Distribution `0.2.1`.

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

Status: signed publication API, schema-v2 config-rule validation, refined
`v16` editor and official version catalog are deployed. The offline signer is
merged to `agent/integration-current` but not yet `main`; no synthetic profile
has been published to the live server yet.

The service API and browser workflow create a global profile, select a local
folder, inspect additions/changes/removals, and stage a new immutable revision.
The refined form generates profile/revision identifiers, validates Minecraft
and NeoForge selections against official release catalogs, and edits a compact
branching file tree with drag-and-drop, folder creation, file editing and
line-level config rules.
Profile history also supports promoting a revision to `stable`, which the
overview displays. The separate local signer must validate and sign canonical
requests; the admin browser must never handle the private key. Validate
publication with a synthetic pack and verify the service rejects invalid
signatures/hashes without exposing a partial revision.

Use the existing SQLite/CAS and signed-revision design. Preserve immutable
history, pinned parent references, anti-rollback checks, path validation,
deduplication, and authorization checks. A failed publication must leave no
partially visible revision. Start with a small synthetic profile that exercises
one mod, one config, one resource pack, and one removal.

### 3. Branch graph and effective profile resolution

Status: core direct-global reconciliation is integrated in Pandora. Option
rule transaction support is in Pandora PR #76; its CI checks passed, and its
remaining gate is a signed synthetic publication plus live client application.
Global-to-global and global-to-local setting inheritance is represented in
schema v2. Multi-key release verification is integrated in Pandora PR #77.

Allow multiple child profiles per parent, with one pinned parent revision per
child revision. Support global-to-global and global-to-local derivation on the
client; local-to-local derivation remains client-local. Resolution reports the
effective inherited tree plus each entry's source and policy without exposing
private local overlays to the server.

Represent add, replace, remove, and policy changes in revision history. A
child can replace/remove an inherited mod or other path. Configuration policies
distinguish enforced values, seed-once defaults, and explicit selectors for
TOML dotted keys, Java properties keys, and `.txt` physical lines. Reject
ambiguous selectors at publication time. Avoid format-agnostic text replacement.

### 4. Client protocol and operational hardening

Public-profile discovery, immutable revision resolution, authenticated object
downloads, protocol version, and capability reporting are integrated. The live
LAN HTTPS endpoint is reachable, but the profile catalog is empty. Next, publish
a synthetic root/child chain, validate client connection and reconciliation
against it, then add backup/restore, retention, audit-safe publication
diagnostics, and a loopback/LAN deployment smoke path. The service still stores
only global profile data and never receives a client filesystem listing.

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

## Key recovery acceptance

Generate a second signer on a replacement admin PC, add its public key to the
server allowlist and Pandora's trusted signer set while retaining the original
key, then publish the next revision of the same profile. Verify that the new
revision is accepted, old revisions still verify, and an unknown signer is
rejected. The server never receives either private key.
