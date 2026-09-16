# Administrative UI development shell

This repository contains a deliberately non-functional administrative UI shell. It is not a player or launcher UI, and it is not an authentication mechanism.

## Activation and exposure

The UI is disabled unless the process is started with `--dev-admin-ui`. When that flag is present, the process refuses to start unless `--listen` contains a literal loopback IP (`127.0.0.0/8` or `::1`). The shell is therefore intended only for local development while the real domain, storage, and authentication layers are incomplete.

The UI does not enable CORS. Its handler accepts only `GET` and `HEAD`; there are no HTML forms and no routes that publish revisions, upload objects, promote channels, or perform rollback.

Example local-only invocation:

```bash
./bootoptim-distribution --listen 127.0.0.1:8088 --dev-admin-ui
```

Then open `http://127.0.0.1:8088/admin/` locally.

## View-model boundary

`internal/adminui.ReadModel` is the typed boundary between this shell and the future administrative domain adapter. The shell currently wires `EmptyReadModel`, which returns no profile data and contains no community fixtures.

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

Those projection endpoints are not implemented by this change. They must be authenticated as administrator endpoints and must preserve hidden-not-found behavior where applicable.

The actual state-changing contract remains the Pandora PR #35 contract and must not be redefined by the UI:

```text
PUT  /v1/admin/objects/sha256/{sha256}
POST /v1/admin/revisions
POST /v1/admin/profiles/{profile_id}/channels/{channel}/promote
POST /v1/admin/profiles/{profile_id}/channels/{channel}/rollback
```

A later UI may invoke those endpoints only after the domain implementations and authentication boundary exist. It must never sign a release in-browser or receive release private keys.

## Authentication boundary

Serving an admin page is not authentication. Production enablement requires an explicit administrator authentication and reverse-proxy policy before the UI is exposed beyond local development. The Pandora contract defines dedicated short-lived OIDC-compatible API credentials, an `admin` role, and optional reverse-proxy mTLS for `/v1/admin/**`; Microsoft/Minecraft game tokens are not distribution credentials.

Release signing remains separate from administrator authentication. The service/UI may validate signed material, but the Ed25519 release private key remains off the service host and outside this repository, environment, database, browser, and proxy configuration.

## Blocked dependencies

This shell intentionally stops before functional administration. It is blocked on:

- Agent 191: signed revision types/validation, pinned inheritance and anti-rollback domain behavior;
- Agent 192: durable SHA-256 CAS and SQLite metadata; and
- a later authentication/reverse-proxy implementation before any production UI exposure.
