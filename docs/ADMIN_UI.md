# Administrative UI

## Publicar una actualización (0.2.19)

En **Perfiles globales**, pulsa **Publicar actualización** en el perfil existente.
El panel carga su última revisión publicada, conserva su identificador, sus
archivos, reglas y permisos, y calcula la siguiente secuencia automáticamente.
La referencia a la madre global permanece fijada: actualizar no añade otro
nivel de herencia ni crea otro perfil.

1. Usa **Añadir archivos**, arrastra archivos, o edita los existentes para cambios
   parciales. Los archivos no elegidos se conservan.
2. Usa **Importar carpeta** para comparar un pack completo: los mods y configs
   ausentes se proponen como eliminaciones. Los objetos propios de este perfil
   también pueden retirarse; un objeto genérico heredado sigue sin poder
   excluirse porque el protocolo no dispone de esa operación.
3. Pulsa **Revisar archivos y cambios** o el botón ⇄ de un archivo de texto.
4. **Preparar archivos** sube únicamente los nuevos o modificados. Revisa el
   resumen, marca la confirmación y pulsa **Publicar actualización**.

La casilla **Activar esta versión para los launchers al publicar** está marcada
por defecto. La publicación y la activación del canal estable son operaciones
separadas; si falla la segunda, el panel informa de que la revisión ya se publicó
y permite activarla desde **Ver revisiones**, sin volver a publicar. Para otra
actualización, abre nuevamente el flujo desde la lista. Las publicaciones
concurrentes conservan la validación de secuencia y la protección del servidor.
La presentación mutable (nombre, descripción e icono) sigue en **Editar perfil**.

## Visor de archivos y cambios

**Archivos y cambios** abre el último perfil publicado con su explorador de
carpetas, búsqueda y filtro de diferencias. A la izquierda está el perfil o
borrador; a la derecha se elige una madre/abuela hasta ocho niveles o una revisión
anterior del mismo perfil. El borrador de actualización compara inicialmente
con su versión anterior. Los archivos eliminados también aparecen.

Los archivos de texto muestran líneas alineadas, números, colores y un mapa de
cambios clicable. Las columnas se desplazan juntas verticalmente y por separado
horizontalmente; el separador se arrastra o se ajusta con las flechas del teclado.
Hay margen inferior para las barras horizontales. Los binarios se comparan por
SHA-256. El visor limita texto a 1 MiB y muestra hasta 10.000 filas por archivo;
el algoritmo usa LCS acotado y cede periódicamente al navegador. Los contenidos
se descargan bajo demanda, verifican SHA-256 y tienen una caché temporal acotada.
La comparación de texto muestra los bytes publicados; las reglas por parámetro
se consultan y modifican en el editor de configuración del borrador.

The admin UI is an operator panel, not a player or launcher UI. It can run in
the loopback development mode, the legacy read-only direct-LAN HTTP mode, or
the authenticated HTTPS mode. Profile publication and other state-changing
routes are available only through authenticated HTTPS.

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

Optionally, pass this flag to enable verification of signed releases:

```text
--release-public-keys-file <trusted-release-public-keys.json>
```

There is no default administrator identity. The public-key file is optional
while bringing up the panel; without it, signed publication fails closed. Its
JSON object maps each signer key ID to a 32-byte unpadded base64url Ed25519
public key. The password verifier file uses the
format documented in `ADMIN_UI_LAN.md`; the cleartext password is never stored
by Distribution. The TLS key is only the server transport key. Release-signing
Ed25519 private keys remain outside the service host, repository, database and
browser. Use the local `bootoptim-release-signer` utility to create keys and
sign downloaded requests on an administrator PC. The service can trust multiple
key IDs simultaneously so a replacement key does not invalidate old revisions.

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

The profile form's Minecraft and NeoForge selectors read
`GET /admin/api/game-versions`, which requires the same administrator session.
Distribution filters Minecraft to official releases from Mojang's version
manifest and reads NeoForge release versions from NeoForged Maven metadata.
The service caches a complete catalog in memory for six hours and continues to
serve its last complete copy during a temporary upstream outage. Catalog
responses are bounded and never accept client-selected upstream URLs.

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
storage. The process wires `SQLiteReadModel` for storage metrics and overview
data, while the signed profile API supplies verified profile history. The UI is
not a source of truth for signature validity, inheritance validity,
anti-rollback, object existence, or authorization.

The presentation model exposes only administrative projections such as profile
identity, immutable revision IDs/sequences/hashes, exact pinned inheritance,
change summaries and storage aggregates. It never receives a release private
key.

## Profile distribution API

The protocol authority remains `PROFILE_PROTOCOL.md`. In HTTPS mode, launcher
clients on the permitted CIDR can read `GET /v1/profiles`, signed revision
envelopes and published objects. Admin-session and CSRF checks protect object
staging and signed revision publication. The API verifies the signer key,
manifest digest, object hashes, pinned inheritance and anti-rollback rules
independently of browser code. Channel promotion/rollback is implemented in the
API, while the panel workflow for those operations remains a later step.
