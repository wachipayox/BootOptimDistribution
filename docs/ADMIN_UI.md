# Administrative UI

The admin UI is an operator panel, not a player or launcher UI. The current
integrated panel is a read-only development shell that can run on loopback or
directly on one private LAN interface, as described in `ADMIN_UI_LAN.md`.
The target panel adds global-profile publication and management.

## Activation and exposure

The UI is disabled unless the process is started with `--dev-admin-ui`. When
that flag is present, the process refuses to start unless `--listen` contains a
literal loopback IP (`127.0.0.0/8` or `::1`). The flag name is retained from the
initial development shell; the page now reads SQLite revision/CAS metadata.

For current direct trusted-LAN read-only access, use `--admin-ui-lan` instead and set both
`--listen` to a literal private address and `--admin-ui-allow-cidr` to the
trusted subnet. That mode binds only the selected interface and denies requests
whose source IP is outside the CIDR. It does not add login or TLS; see
`ADMIN_UI_LAN.md` for the tradeoff and firewall setup.

The UI does not enable CORS. Its handler accepts only `GET` and `HEAD`; there
are no routes that publish revisions, upload objects, promote channels, or
perform rollback. In LAN mode the service binds only to a private address and
checks every request's source IP against the configured private CIDR. This is
network-level restriction, not user authentication or encryption. Use that mode
only on a trusted LAN and do not port-forward it. This current HTTP mode must
remain read-only.

Example local-only invocation:

```bash
./bootoptim-distribution --listen 127.0.0.1:8088 --dev-admin-ui
```

Then open `http://127.0.0.1:8088/admin/` locally. For direct LAN access, follow
`ADMIN_UI_LAN.md` and use the configured private address.

## View-model boundary

`internal/adminui.ReadModel` is the typed boundary between presentation and
storage. The process currently wires `SQLiteReadModel`, which projects stored
revision manifests and CAS aggregates into a read-only overview.

The presentation model is intentionally narrower than the signed wire/persistence model. It is expected to expose only already-authorized administrative projections:

- official profile identity and visibility;
- channels and their immutable current revision heads;
- immutable revision id, monotonic sequence, manifest SHA-256 and publication time;
- exact pinned inheritance (`profile_id`, `revision_id`, `manifest_sha256`), never `latest`;
- summarized additions/removals/overrides; and
- channel rollback status.

The UI must never become a source of truth for signature validity, inheritance validity, anti-rollback, object existence, or authorization. Agents implementing those domains own those decisions.

## Future endpoint contract

Once the signed revision and storage domains exist, the administrative HTTP layer may adapt them to read-only UI projections. The UI needs the equivalent of:

```text
GET /v1/admin/ui/overview
GET /v1/admin/ui/profiles/{profile_id}/revisions
GET /v1/admin/ui/revisions/{revision_id}
```

The current local panel serves its projection at `/admin/api/overview`. Future
routes must preserve hidden-not-found behavior where applicable and require
Distribution-native HTTPS, administrator login/session, and CSRF protection.
Do not add Caddy or require an external reverse proxy.

The actual state-changing contract remains the Pandora PR #35 contract and must not be redefined by the UI:

```text
PUT  /v1/admin/objects/sha256/{sha256}
POST /v1/admin/revisions
POST /v1/admin/profiles/{profile_id}/channels/{channel}/promote
POST /v1/admin/profiles/{profile_id}/channels/{channel}/rollback
```

A later UI may invoke those endpoints only after the domain implementations
and authentication boundary exist. It must never sign a release in-browser or
receive release private keys. The local signer is a separate tool and returns
only a signed envelope to the panel.

## Authentication boundary

Serving an admin page is not authentication. The current direct LAN mode has
no user authentication and no TLS; all devices in the configured trusted
subnet can read the panel over HTTP. It is deliberately read-only. Before
adding mutations, implement Distribution-native HTTPS and administrator
login/session with CSRF protection. Microsoft/Minecraft game tokens are not
Distribution credentials. No Caddy or third-party identity provider is in
scope for the private LAN panel.

Release signing remains separate from administrator authentication. The service/UI may validate signed material, but the Ed25519 release private key remains off the service host and outside this repository, environment, database, browser, and proxy configuration.

## Current implementation status

Signed revision validation, pinned inheritance, anti-rollback primitives,
durable SHA-256 CAS, SQLite metadata, and a read-only panel are already present
in the current integration branch. HTTP publication, distribution reads,
administrator login/HTTPS, and browser profile creation are not yet
implemented. See `ROADMAP.md` and `PROFILE_PROTOCOL.md` for the agreed next
work.
