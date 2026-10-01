# Distribution service roadmap

`agent/integration-current` is the service integration authority; `main` alone
is deployable. On 2026-09-30 the live service is Distribution `0.2.12`
(`1f3bc297`). Release `0.2.13` (`2ae19aa`) is on `main`, but its panel-driven
deployment failed: the old updater tried to rewrite `/etc/systemd/system`
while its systemd sandbox mounts that directory read-only. Release `0.2.14`
removes that runtime unit rewrite and refreshes only the helper under
`/usr/local/libexec`. Because the installed 0.2.12 helper cannot update itself
under this restriction, one manual `scripts/update.sh --apply` and service
restart will be required after 0.2.14 is on `main`; subsequent updates can use
the panel. The authenticated native-HTTPS admin panel, LAN-restricted
profile reads, signed publication API, schema-v2 setting-rule validation,
searchable official Minecraft/NeoForge versions, and the panel-driven service
updater are deployed. The updater was exercised from the panel and reported
the live service current at `0.2.12`.

Two signed synthetic profiles are now published on the live server for the
end-to-end check: root `profile_wachiland-config-rules-e2e-root` and child
`profile_wachiland-config-rules-e2e-child`. They contain only generated test
bytes, not player files. The child pins the root revision and replaces config
rules in TOML, Java properties, and TXT while also changing/removing/adding
synthetic pack entries.

The first real Pandora client read exposed an API serialization bug: profiles
without a stable channel currently return `channels: null`, which the client's
non-optional list cannot decode. Release `0.2.13` fixes this to emit
`channels: []`, renews the hidden revision ID after successful publication,
and displays nested API error messages correctly. The client reconciliation
PR #76 has passing CI and local focused tests, but remains unmerged until the
same live root/child chain passes the client E2E harness after the server fix
is deployed. The client harness still fails decoding a null channel list until
the one-time updater bootstrap installs 0.2.14 or later.

The offline signer and its recovery/rotation tests are included in deployable
`main`. The signer runs on the administrator PC; it is not required on the
Linux service. The panel lets the operator point a profile's `stable` channel
at a published revision from its history.

File schema policies are `enforced` and `default_once`. Manifest schema v2
adds per-setting TOML, `.properties`, and `.txt` rules in the panel, API, and
server validator. Pandora PR #76 adds transactional client merging and repair
support, including descendants replacing rules inherited from ancestors. Its
focused tests and CI are green; live-client verification is the remaining
gate. The broader `user_owned` file policy and arbitrary structured formats
remain out of scope.

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
- Automatic server signing replaces mandatory workstation signing by explicit
  user decision on 2026-10-01. Keep private identity only in persistent server
  state, never the browser, launcher, repository or logs.
- Preserve public identity history during regeneration so old revisions remain
  verifiable. Replacing the administrator PC requires no key migration.
- The first end-to-end publication uses a small synthetic pack, not real player
  data. The browser selects a source folder and previews the proposed changes.
- A revision records its parent pin and its own changes/removals. Profile
  history, not a full client disk scan, defines which files an update needs to
  apply.
- Keep the default service listener loopback until a deliberate LAN
  configuration enables the authenticated HTTPS endpoint.

## Delivery sequence

### 1. Authenticated LAN administration

Status: implemented and deployed as Distribution `0.2.12`.

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
editor, official version catalog and offline signer are deployed. Synthetic
root and child profiles have been signed and published on the live server.
The client E2E read is currently blocked by `channels: null` on profiles
without a stable channel. The fix is on `main` as `0.2.13`; the old updater
cannot deploy it under its current filesystem sandbox, so a one-time manual
bootstrap is required after the updater fix is promoted.

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
rule transaction support is in Pandora PR #76; its CI and local focused tests
passed. The live synthetic publication is complete; client application is
pending after the server fixes the nullable-channel response.
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
LAN HTTPS endpoint is reachable and the signed synthetic chain is published.
The client E2E harness currently fails while decoding `channels: null`; deploy
the `0.2.13` server fix, rerun the harness, and then integrate Pandora PR #76.
Follow with backup/restore, retention, audit-safe
publication diagnostics, and a loopback/LAN deployment smoke path. The service
still stores only global profile data and never receives a client filesystem
listing.

## Acceptance path

1. Sign in over HTTPS and prepare the synthetic root/child profiles in the
   admin panel. **Complete:** both signed revisions are published and the
   child pins the root; no player data is involved.
2. Have Pandora resolve the signed root and child, apply config setting rules
   transactionally, and preserve locally edited `default_once` values on a
   later merge. **Pending:** rerun the live client harness after deploying the
   server's `channels: []` fix.
3. Have Pandora install and update a derived test profile using only the
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

## Presentation and launcher catalog — 2026-10-01
Implemented on `codex/profile-icons`: signed optional profile description/icon,
PNG validation and CAS publication, icon selection during creation, presentation
editing through a separately signed next revision. Launcher work adds a main-page
carousel and profile detail dialog with install action. Linux Go suite passes;
live deployment and client artwork round-trip are pending.

2026-10-01: presentation editing supersedes revision-based artwork editing.
Name/description/icon now have a mutable catalog override; game revisions stay
immutable. API regression test proves unchanged sequence and CAS icon lifecycle.

2026-10-01 follow-up: panel presentation editor initializes the form from the
mutable presentation object (including its existing icon), falling back to
signed profile metadata for never-edited profiles. Asset query is bumped to
profile-presentation-0216.


## Publication policy override — 2026-10-01

The user explicitly replaced mandatory workstation signing with automatic
server signing for the private friends-only modpack. This supersedes earlier
private-key-on-PC requirements in this historical roadmap. Implemented in
0.2.17: authenticated CSRF publication, persistent server identity/public
history, unchanged immutable revision validation, HTTPS public-key discovery.
