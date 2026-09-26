# Administrative UI

The admin UI is an operator panel, not a player or launcher UI. The embedded
panel remains read-only. It can still run in the existing loopback development
mode or in the legacy direct-LAN HTTP mode, but state-changing admin routes must
use the authenticated HTTPS boundary described below.

## Exposure modes

The process has three mutually exclusive admin UI modes:

- `--dev-admin-ui`: loopback-only HTTP for local development.
- `--admin-ui-lan`: the existing direct private-LAN HTTP mode. It requires a
  private literal `--listen` address plus `--admin-ui-allow-cidr` and remains
  read-only and unauthenticated.
- `--admin-ui-https`: Distribution-native HTTPS with local administrator login.
  It requires the same private bind/CIDR restriction plus an explicit server
  certificate/key and local administrator verifier.

The default listener remains `127.0.0.1:8088`. No mode accepts `0.0.0.0` for a
LAN admin listener, and the CIDR filter uses the socket peer address rather than
`X-Forwarded-For`. CIDR filtering is defense in depth, not authentication.

The HTTPS mode requires all of these options:

```text
--listen <private-ip:port>
--admin-ui-https
--admin-ui-allow-cidr <private-cidr>
--tls-cert-file <server-certificate-chain.pem>
--tls-key-file <server-private-key.pem>
--admin-username <local-admin-name>
--admin-password-hash-file <0600-password-verifier-file>
```

There is no default administrator identity. The password verifier file uses the
format documented in `ADMIN_UI_LAN.md`; the cleartext password is never stored
by Distribution. The TLS key is only the server transport key. Release-signing
Ed25519 private keys remain outside the service host, repository, database and
browser.

## Login and session boundary

`--admin-ui-https` serves HTTPS directly from the Go service with TLS 1.3 or
newer. `/admin/login` is a local form login. Successful authentication creates
an opaque random server-side session with an eight-hour absolute lifetime.
Sessions are memory-only and are invalidated by service restart.

The session cookie is host-only (`__Host-` prefix), `Secure`, `HttpOnly`,
`SameSite=Strict`, and `Path=/`. Login has its own CSRF cookie and form token.
Credential failures return the same generic error whether the username or
password was wrong.

The existing panel and `/admin/api/overview` require the authenticated admin
session in HTTPS mode. `GET /admin/api/session` returns the authenticated
principal and that session's CSRF token for same-origin browser code. Logout is
`POST /admin/logout` and also requires the session CSRF token.

The legacy `--admin-ui-lan` mode deliberately does not gain login or TLS in this
change. Its handler remains GET/HEAD-only so the old trusted-LAN read path does
not become an unauthenticated mutation boundary.

## Admin API middleware contract

Profile API code should import `internal/adminauth`; it does not need to know
how passwords or sessions are stored.

1. The server places `Manager.Authenticate` outside the mux so a valid session
   becomes a request-context principal.
2. Any admin read route wraps its handler with `adminauth.RequireAdmin`.
3. Any state-changing admin route additionally wraps the handler with
   `manager.RequireCSRF`. The browser sends the token in `X-CSRF-Token`.
4. Domain code can read the authenticated identity with
   `adminauth.PrincipalFromContext(ctx)`. The only role currently issued by the
   local login is `admin` (`adminauth.RoleAdmin`).

Composition for a future mutation route is intentionally small:

```go
mux.Handle("/v1/admin/revisions",
    adminauth.RequireAdmin(manager.RequireCSRF(revisionHandler)))
```

That mux must itself be served through `manager.Authenticate(...)`, as the
current HTTPS admin wiring already does. `RequireAdmin` returns `401` for an
anonymous request. `RequireCSRF` returns `403` for an unsafe request without
the per-session token. Neither middleware treats a matching client CIDR as an
identity or role.

## View-model boundary

`internal/adminui.ReadModel` remains the typed boundary between presentation and
storage. The process wires `SQLiteReadModel`, which projects already-persisted
revision manifests and CAS aggregates into a read-only overview. The UI is not
a source of truth for signature validity, inheritance validity, anti-rollback,
object existence, or authorization.

The presentation model exposes only administrative projections such as profile
identity, immutable revision IDs/sequences/hashes, exact pinned inheritance,
change summaries and storage aggregates. It never receives a release private
key.

## Future profile routes

The protocol authority remains `PROFILE_PROTOCOL.md`. State-changing routes
such as object staging, revision publication, promotion and rollback are not
implemented by this change. When the profile API lands, its mutation handlers
must use the admin-session + CSRF contract above and must continue to enforce
the signed revision, object hash, pinned inheritance and anti-rollback domain
rules independently.
