# Private profile protocol contract (v1/v2/v3/v4)

This is the shared boundary for Distribution admin/publishing and Pandora
profile discovery/update. It is the product contract for the first service
implementation; changes must update both this document and the Pandora
integration plan. The authenticated profile API and browser publication panel
are present on `agent/integration-current`; `main` remains the deployable
release branch.

**Terminología (0.3.0).** Lo que el protocolo llama *channel* (rutas
`/channels/{name}/…`, campos JSON `channels`, tablas SQL y el valor `stable`)
se llama en el panel, los mensajes y estos docs **revisión estable**. Las rutas,
los campos y el valor `stable` no cambian. Así no se confunde con el *canal del
launcher* (`player`/`developer`), que es otra cosa.

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

The current validator accepts file policies `enforced` and `default_once`.
Manifest schema v2 additionally supports `config_settings`: TOML dotted keys,
Java properties keys, and physical `.txt` line selectors with `enforced` or
`default_once` values. Child revisions replace a matching `(path, key)` selector
after checking the immediate parent's config permissions. Unsupported or
ambiguous selectors are rejected. Unselected lines and values remain local.
The broader `user_owned` file policy is still not part of the accepted schema.

The launcher applies these setting rules while reconciling the selected
revision inside its existing per-profile transaction. Enforced changes keep a
recoverable copy of the previous local file; `default_once` keys are seeded
only once and preserve later user edits. The server validates the signed shape
and inheritance permissions; the client validates and merges the text format.

## HTTP shape

Access policy in HTTPS mode (`--admin-ui-https`, since 0.2.25 for reads and
0.3.0 for the test API):

| Routes | Who can reach them | Authentication |
|---|---|---|
| `GET`/`HEAD` `/v1/...` except `/v1/admin/` and `/v1/test/`, `/healthz` | any address (players outside the LAN) | none; installation trusts the signed revision |
| `/v1/test/...` (any method) | any address | shared test token (`Authorization: Bearer`), see «Perfiles de prueba» |
| `/v1/admin/...`, `/admin/...` | only `--admin-ui-allow-cidr` (LAN) | admin session cookie + CSRF |

In the legacy modes every route stays on the LAN CIDR. The default listener is
still loopback.

All state-changing admin routes require authenticated administrator session and
CSRF protection over Distribution-native HTTPS. As explicitly requested on
2026-10-01, signing is automatic in the service's persistent private data
folder. Browser and launcher never receive the private key. This supersedes
the earlier workstation-only signing policy: publication authority now follows
admin authentication and verified HTTPS server identity.

Public/client read routes (reachable outside the LAN in HTTPS mode):

```text
GET /v1/signing-keys  # schema_version: 1, keys: {id: base64url public key}
GET /v1/profiles      # test profiles hidden unless ?include_test=1
GET /v1/profiles/{profile_id}/revisions/{revision_id}   # any profile, test or not
GET /v1/objects/sha256/{sha256}
GET /v1/news                                     # 0.4.0, «Diseño de los launchers»
GET /v1/profiles/{profile_id}/changelog          # 0.4.0
GET /v1/launcher/{app}/releases                  # 0.4.0
```

`GET /v1/profiles` lists every live profile (since 0.3.0 it no longer truncates
by revision count; `truncated` refers to profiles).

Implemented administrator routes:

```text
GET  /v1/admin/profiles
GET  /v1/admin/profiles/{profile_id}/revisions
POST /v1/admin/objects/sha256/{sha256}                 # authenticated staged upload
POST /v1/admin/publications                           # validated request, server signs
POST /v1/admin/revisions                              # signed immutable envelope
POST /v1/admin/profiles/{profile_id}/channels/{name}/promote   # revisión estable
POST /v1/admin/profiles/{profile_id}/channels/{name}/rollback
PUT  /v1/admin/profiles/{profile_id}/presentation
DELETE /v1/admin/profiles/{profile_id}
GET  /v1/admin/test-api                               # token state, quotas, test profiles
POST /v1/admin/test-api/token                         # generate/rotate the shared test token
```

The admin API rejects with `403 test_profile_admin_write` any publication,
presentation or stable-revision change on a `profile_test-…` id (it may only
delete them), and with `403 test_parent_not_allowed` a normal profile whose
`base` is a test profile.

Profile creation is the publication of a valid root revision whose profile ID
does not yet exist. Child publication pins its parent in the manifest. Uploads
that have not yet been referenced are not visible as a profile/revision and
must be eligible for later orphan collection. Promotion/rollback changes only
the stable-revision state; it never mutates the published revision.

Presentation-only read-model routes:

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
3. The panel stages changed bytes, computes a canonical manifest digest and
   asks the administrator to review and confirm publication.
4. `POST /v1/admin/publications` accepts `{canonicalization, manifest_sha256,
   manifest}`, validates the schema/digest, signs canonical bytes and passes
   the envelope to the existing signature/object/parent/sequence checks.
5. Publication becomes visible only after the existing atomic store succeeds.
   The legacy signed-envelope endpoint remains compatible.

## Global children always follow their parent (0.2.22)

User decision (2026-10-14), with no opt-out: when a **new** revision of a
profile is published (`201`; an identical replay `200` triggers nothing), the
service finds every global profile whose latest revision inherits from an
older revision of that profile and publishes for each one a new revision with
the same own content, `sequence + 1`, a random `revision.id`, the parent's game
versions and the base re-pinned to the parent's exact new
`(profile_id, revision_id, manifest_sha256)` tuple. Its release note is
`auto: sigue a <profile> rev <revision>`. This cascades to grandchildren up to
`MaxInheritanceBases` (8).

Each automatic revision is a normal immutable signed revision and passes the
same signature, CAS object, inheritance chain and anti-rollback checks. If a
child no longer validates against the new parent it is not published: the
parent's response carries `auto_follow[]` with `status: "skipped"`, `code`
(`auto_follow_conflict` when the child's own rules, e.g. `config_settings`, no
longer fit) and `message`; the admin panel shows it as a warning and the log
gets an `auto-follow: WARN` line. The parent publication never fails because
of its children.

Only exception (0.3.0): a **test profile** registered with `follow_parent:
false` is skipped (logged, not listed in `auto_follow`), and so are its own
descendants through it. Normal profiles still always follow. The inventory used
here is the latest revision of every live profile, with no row limit, so test
revisions can never push a normal child out of it.

Stable revision: if the child's stable revision (`stable`) pointed at the child's previous
latest revision (it was tracking), it is promoted to the automatic revision
through the same forward-promotion checks as a manual promote
(`stable: "promoted"`). If it pointed at an older revision (deliberately held
back) it is left alone (`stable: "stable_held"`). A failed promotion reports
`stable: "promote_failed"` with `stable_message`.

## Release-key recovery and rotation

The service creates `signing/server-release-key.json` in its data directory
(mode 0600; folder 0700). `signing/public-keys.json` preserves all generated
public identities. Restart reuses the private identity; malformed identity
fails startup without replacement. If an operator explicitly removes a lost
private identity, restart generates another while retaining public history.
Back up the complete data directory together with the legacy key map.

`--release-public-keys-file` remains the legacy JSON public-key map. The
HTTPS/CIDR read endpoint `/v1/signing-keys` exposes its union with automatic
public identities, not private key material. Pandora discovers unknown signers
through the configured HTTPS origin (no redirects; bounded document and key
count), still verifies signatures and rejects disagreement with a locally
pinned key. Losing the administrator PC requires no key migration. Older
launchers must be updated before consuming automatically signed revisions.

## Profile presentation (2026-10-01)
`profile.description` is optional UTF-8 text (maximum 8192 bytes). `profile.icon`
is an optional signed CAS object reference `{sha256, size, media_type}`; only
PNG up to 2 MiB and dimensions 1–1024 are accepted. The panel converts selected
PNG/JPEG/WebP to a 256 × 256 transparent PNG. Icons are published objects but
have no game-file destination. Clients verify the revision signature and object
hash before decoding them. Old revisions without either field remain valid.
The catalog includes description, Minecraft, NeoForge and icon metadata for UI
presentation; installation authority remains the signed revision.

Presentation edits publish a new monotonically increasing revision of the same
profile, retaining its exact game identity, base pin, files and rules. They use
the same automatic signing workflow as modpack changes. Editing never rewrites an
existing immutable revision. Promoting the stable revision remains an explicit separate
operation; a launcher following `stable` sees the edit after promotion.

## Mutable profile presentation (2026-10-01)

> 0.4.0: el cuerpo de la escritura (actualización parcial, artwork, logo,
> `android_compatible`) y la forma del catálogo cambian; ver «Diseño de los
> launchers», apartado 2.

`PUT /v1/admin/profiles/{profile_id}/presentation` uses the existing authenticated
admin session, LAN policy and CSRF protection. Body: `{name, description, icon}`;
icon is null or a verified PNG CAS reference (2 MiB, at most 1024x1024).
Name/description/icon are mutable catalog presentation, not game revisions.
No sequence, stable revision, parent pin or signed game manifest is changed, and no
release private key is required. `/v1/profiles` includes a `presentation` override.
Older profiles fall back to signed metadata until first edit; later game updates
do not replace the override. The launcher trusts HTTPS for cosmetic presentation,
and still verifies signed revisions for installation/game content. Cosmetic
icons are bounded and hash-verified before decoding. Removing an icon withdraws
its public CAS availability unless a signed revision still references it.

Panel: Perfiles globales -> Editar perfil -> Nombre, Descripción, Icono ->
Guardar cambios. Initial creation includes an optional Elegir imagen control;
subsequent edits do not require signing or publishing a new game revision.
SQLite profile_presentation lives in the existing metadata database and must be
included with its ordinary backup. Concurrent presentation edits are last-write-wins.

2026-10-01 follow-up: panel presentation editor initializes the form from the
mutable presentation object (including its existing icon), falling back to
signed profile metadata for never-edited profiles. Asset query is bumped to
profile-presentation-0216.


## Global profile deletion (0.2.18)

`DELETE /v1/admin/profiles/{profile_id}` requires the existing administrator
session, LAN boundary and CSRF protection, plus a strict JSON body
`{confirm_profile_id: "the exact profile_id"}`. A mismatch is rejected.
The panel requires entering that identifier before enabling its final action.

Deletion is a durable catalog tombstone (`deleted_profiles`), not deletion of
immutable signed history or shared CAS files. The profile disappears from both
admin/client catalog and recent publication lists. Existing exact revision pins
remain readable so local installations and derived profiles keep their valid
ancestry. Deleted identifiers cannot receive publications or stable-revision changes
and cannot be reused for a different profile. It is idempotent. No filesystem
or player-world deletion is performed.

## Parámetros JVM del perfil (esquema 3, 2026-10-14)

`schema_version: 3` es el esquema 2 más dos campos obligatorios:
`jvm_options` (lista, puede ir vacía) y `remove_jvm_options` (lista de ids).
`config_settings` sigue siendo obligatorio desde el esquema 2. Los esquemas 1 y 2
no pueden llevar parámetros JVM. **Los launchers que solo aceptan 1–2 rechazan
una revisión 3** (comportamiento previsto: fallan en cerrado). Antes de publicar
una revisión 3 en un perfil real, Pandora y el launcher Android deben aceptar el
esquema 3.

### Forma

```json
{
  "id": "jit-compiler-threads",
  "label": "Hilos del compilador JIT",
  "template": "-XX:CICompilerCount={value}",
  "type": "int", "min": 2, "max": 4,
  "help": "Qué hace exactamente el parámetro (tooltip).",
  "java": { "min": 21 },
  "vm": ["hotspot"],
  "platforms": {
    "pc": {
      "default_enabled": false,
      "default_value": 2,
      "auto": {
        "rules": [ { "when": { "logical_cpus": { "max": 4 } }, "enabled": true, "value": 2 } ],
        "otherwise": { "enabled": false }
      }
    }
  }
}
```

| Campo | Regla |
|---|---|
| `id` | `^[a-z0-9][a-z0-9-]{0,63}$`, único en el manifiesto. Es la clave con la que el launcher guarda la elección del usuario. |
| `label` | 1–64 bytes, una línea. |
| `template` | Produce **un solo argumento**. `bool`: sin `{value}`. `int`/`enum`: exactamente un `{value}`. No admite otras llaves. |
| `type` | `bool`, `int` (`min`/`max` obligatorios, 32 bits) o `enum` (`values`: 1–16, `[A-Za-z0-9._+-]`, sin repetir). |
| `help` | 1–1024 bytes. |
| `java` | `min` ≥ 8; `max` opcional (inclusivo). Versión de Java por número de *feature release*. |
| `vm` | Subconjunto de `hotspot`, `openj9`. Obligatorio si la plantilla es `-XX:`. Si falta, vale cualquier VM. |
| `platforms` | Claves `pc` y/o `android`. Cada plataforma lleva **sus propios** valores; nunca se comparten. Un launcher ignora las plataformas que no son la suya. |
| `default_enabled` | Obligatorio. |
| `default_value` | Obligatorio para `int`/`enum`, prohibido para `bool`. |
| `auto` | Opcional. "Decidir según el equipo": gana la primera regla cuyo `when` se cumpla entero; si ninguna, `otherwise`. 1–16 reglas. Datos admitidos: `logical_cpus`, `memory_mib` (RAM física total), cada uno con `min` y/o `max` inclusivos. Si una decisión activa una opción `int`/`enum`, lleva `value`. |

No se admite `null` dentro de `jvm_options`. Como máximo 64 opciones y 64 ids
eliminados por manifiesto.

### Lista blanca de argumentos

Se comprueba al publicar (Distribution) **y otra vez en cada launcher antes de
lanzar**, aunque la revisión esté firmada, sobre el argumento ya renderizado con
cada valor posible:

- `-XX:+Nombre`, `-XX:-Nombre`, `-XX:Nombre=<entero | decimal | tamaño k/m/g/t>`.
  Nunca valores de texto (así no caben rutas ni comandos: `OnError`,
  `OnOutOfMemoryError`, `ErrorFile`, `HeapDumpPath`…). Nombres prohibidos
  aunque tengan valor numérico o booleano: `Flags`, `VMOptionsFile`,
  `IgnoreUnrecognizedVMOptions`, `EnableJVMCI`, `EnableJVMCIProduct`,
  `UseJVMCICompiler`, `UseJVMCINativeLibrary`, `LogVMOutput`,
  `ShowMessageBoxOnError`, `PauseAtStartup`, `PauseAtExit`, `ManagementServer`.
- `-Xss<tamaño>`, `-Xms<tamaño>`, `-Xmx<tamaño>`.
- `-Dclave=valor` solo si la clave está en la lista de propiedades permitidas,
  que hoy está **vacía**.
- Todo lo demás se rechaza: `-javaagent`, `-agentpath`, `-agentlib`, `-cp`,
  `-classpath`, `--class-path`, `--add-opens`, `--add-exports`,
  `--patch-module`, `--module-path`, `@archivos`, `-jar`, `-Xbootclasspath`,
  `-Xlog`, `-verbose`…
- ASCII imprimible, sin espacios, ≤ 256 bytes.

Los casos de prueba compartidos están en
[`jvm-options-vectors.json`](jvm-options-vectors.json). Cada cliente copia ese
archivo tal cual y sus tests deben aceptar y rechazar exactamente lo mismo,
incluida la clave de flag (`flag_key`).

**Añadir una clave `-D`**: añadirla a `AllowedJVMSystemProperties`
(`internal/revision/jvm_options.go`), a la constante equivalente de cada
launcher y a `allowed_system_properties` del archivo de casos, con un caso
aceptado. Un launcher que no la conozca omite la opción con aviso.

### Clave de flag y colisiones

La clave identifica qué ajuste de la JVM toca un argumento: `XX:<Nombre>` para
`-XX:` (con o sin `+`/`-`/`=`), `Xss`/`Xms`/`Xmx`, `D:<clave>`. En el conjunto
efectivo de un perfil no puede haber dos opciones con la misma clave en la misma
plataforma. Todos los valores de una opción deben dar la misma clave.

### Herencia

Igual que `config_settings`, del ancestro más antiguo al descendiente: una
revisión hija sustituye la opción con el mismo `id` y la retira con
`remove_jvm_options` (que debe nombrar una opción heredada; una raíz no puede
retirar nada). No hace falta un permiso del padre: solo publica el admin.

### Contrato para los launchers (PC y Android)

1. Aceptar `schema_version` 3, resolver la cadena como arriba y guardar el
   conjunto efectivo en el estado persistente de la instancia: lanzar nunca
   necesita contactar con el servidor.
2. Volver a validar cada opción con la lista blanca. Ignorar las plataformas que
   no son la propia.
3. Elección del usuario guardada **por instancia y por `id`**, y solo con lo que
   haya tocado (modo `auto`/activado/desactivado y valor). Si no ha tocado nada,
   se sigue el valor por defecto de la revisión vigente (o `auto`, si existe).
   Un valor guardado que ya no encaje en el tipo o rango nuevo se descarta y se
   usa el del perfil. Las opciones que desaparecen del perfil dejan de aplicarse.
4. Si la plataforma, la versión de Java real o la VM real no encajan, o `auto`
   usa un dato que el launcher no conoce, **no se pasa la opción** y se registra
   `[WBOMBO-COMPAT] FALLBACK project=<launcher> mechanism=jvm-option/<id> mod=java@<versión>-<vm> reason=<motivo>`
   (WARN). Al aplicarla, `[WBOMBO-COMPAT] ACTIVE …` (INFO). Una opción `-XX`
   desconocida impide arrancar la JVM, por eso no se arriesga.
5. Orden: argumentos de la versión → opciones del perfil → memoria del usuario
   (`-Xms`/`-Xmx`) → parámetros JVM personales del usuario. En HotSpot gana la
   última aparición, así que lo del usuario manda. Si sus parámetros o su
   configuración de memoria ya tienen la misma clave de flag, la opción del
   perfil no se pasa y la UI la marca como "anulada por tus parámetros". Los
   parámetros personales nunca se modifican.
6. Traza en cada lanzamiento: ids y valores aplicados, desactivados, anulados y
   omitidos (con motivo y, si es automático, los datos usados). Sin rutas ni
   datos personales.

## Modloader del perfil (esquema 4, 2026-10-15)

Hasta el esquema 3, `game` es `{minecraft, neoforge}`. El esquema 4 admite
Fabric y Forge además de NeoForge:

```json
"game": { "minecraft": "1.20.1", "loader": { "kind": "forge", "version": "1.20.1-47.3.0" } }
```

- `kind` ∈ `neoforge`, `fabric`, `forge` (lista cerrada; otro valor se rechaza).
  `version` es la versión del loader tal como la usa el launcher para instalarlo:
  NeoForge `21.1.1`, Fabric `0.16.9` y Forge la versión **completa de Maven**
  (`1.20.1-47.3.0`; en Minecraft antiguo `1.7.10-10.13.4.1614-1.7.10`), de modo
  que Pandora la pasa tal cual como versión preferida. Solo `[A-Za-z0-9._+-]`,
  1–64 bytes, sin empezar por signo.
- En el esquema 4 `game` lleva solo `minecraft` y `loader` (sin `neoforge`); en
  los esquemas 1–3 `game.loader` está prohibido. El esquema 4 incluye todo lo
  del 3 (`config_settings`, `jvm_options`, `remove_jvm_options` presentes).
- **Esquema mínimo:** el panel emite el 4 solo si el loader es Fabric o Forge.
  Un perfil NeoForge sigue saliendo con 1, 2 o 3, de modo que los launchers que
  aún no aceptan el 4 (Android) lo siguen leyendo.
- Herencia: un hijo debe tener el mismo Minecraft y el mismo loader (`kind` y
  `version`) que su madre. Un NeoForge del esquema 3 y uno del 4 se consideran
  el mismo juego si coinciden `kind` y versión.
- Catálogo (`GET /v1/profiles`): cada perfil añade `loader: {kind, version}`
  (siempre presente); `neoforge` se mantiene y va vacío si el loader no es NeoForge.
- Contrato para los launchers: aceptar el 4; si no pueden instalar el `kind`,
  rechazar la revisión con un mensaje claro (no instalar otro loader). Un
  launcher que no conozca el esquema 4 rechaza la revisión como hoy.

## Perfiles de prueba (0.3.0)

Decisión del usuario (2026-10-21): los agentes crean, cambian, publican,
consultan y borran perfiles **de prueba** desde la terminal; los perfiles
normales no se pueden modificar por esa vía. Uso previsto: hijos de prueba del
Beta (`profile_wachiland-elite-beta`). Guía del CLI:
[`PERFILES_PRUEBA_CLI.md`](PERFILES_PRUEBA_CLI.md).

### Marca

- El id empieza por `profile_test-` **y** existe una fila en `test_profiles`
  (`profile_id`, dueño, `created_at`, `follow_parent`). La API de prueba exige
  las dos; cualquier escritura sobre otro id responde
  `403 {"error":{"code":"not_a_test_profile"}}`.
- El id va firmado dentro del manifiesto, así que la marca viaja con cada
  revisión sin cambiar el esquema: Pandora y Android pueden reconocerla por el
  prefijo. Las revisiones de prueba son revisiones normales: inmutables,
  firmadas por el servidor, `official: true`, verificables con la verificación
  existente (`ParseAndVerifyRevisionEnvelope` y `/v1/signing-keys`).
- La API de admin no puede publicar ids `profile_test-…` ni derivar un perfil
  normal de uno de prueba; sí puede borrarlos (panel → Servicio).
- Un perfil de prueba es hijo (o nieto, hasta 8 niveles) de cualquier perfil
  vivo, normal o de prueba. Nada sube de hijo a padre.

### Autenticación

Un único token compartido, siempre obligatorio en `/v1/test/`:

- Se genera y se rota en **Servicio → Perfiles de prueba**. Se muestra una vez;
  el servidor guarda solo su SHA-256 (`test_api_token`) y compara en tiempo
  constante. Rotarlo invalida el anterior al momento. Sin token generado, todo
  `/v1/test/` responde `503 test_token_not_configured`.
- Cabeceras: `Authorization: Bearer <token>` y `X-Wbombo-Agent: <etiqueta>`
  (opcional, `[A-Za-z0-9._-]{1,64}`, se guarda como dueño; es informativa).
- Sin cookie ni CSRF. El token nunca se registra ni se devuelve salvo en la
  respuesta de generación. Token ausente o incorrecto: `401 invalid_token`.
  Tras 20 fallos en 10 minutos desde una IP: `429 too_many_auth_failures`.
- En modo HTTPS `/v1/test/` es accesible fuera de la LAN, como las lecturas
  públicas; `/v1/admin/` sigue solo en la LAN.

### Rutas

```text
GET    /v1/test/profiles[?include_deleted=1]         # dueño, padre, follow_parent, última revisión, bytes propios
POST   /v1/test/profiles                             # {slug, name?, parent_profile_id, parent_revision_id|"latest", follow_parent?}
POST   /v1/test/profiles/{id}/changes                # {notes?, operations:[…]}
POST   /v1/test/profiles/{id}/repin                  # {parent_revision_id|"latest"}
PUT    /v1/test/profiles/{id}/follow                 # {follow_parent: bool}
PUT    /v1/test/profiles/{id}/presentation           # como la del admin
POST   /v1/test/profiles/{id}/channels/{ch}/promote  # {revision_id}
POST   /v1/test/profiles/{id}/channels/{ch}/rollback # {revision_id, reason?}; el servidor firma el rollback
DELETE /v1/test/profiles/{id}                        # {confirm_profile_id}; lápida
GET    /v1/test/profiles/{id}/revisions              # historial de cualquier perfil (solo lectura)
HEAD   /v1/test/objects/sha256/{d}                   # 200 si el CAS ya tiene los bytes
POST   /v1/test/objects/sha256/{d}                   # cuerpo crudo, cuenta en las cuotas
GET    /v1/test/usage                                # cuotas y disco
```

- **Crear** publica la revisión 1: un hijo vacío fijado al padre, con su misma
  versión de juego y loader, y los `permissions` del padre (nunca más
  permisivo). Promueve su revisión estable. `follow_parent` es `true` por
  defecto.
- **Cambios** parte de la última revisión del perfil de prueba, aplica las
  operaciones y publica una revisión nueva por la misma `publishCore` que una
  publicación normal (firma, CAS, cadena, `config_settings`, `jvm_options`,
  anti-rollback). Además respeta `permissions.mods` del padre directo: sin
  `add` no se añaden ni reemplazan mods; sin `remove` no se quitan heredados
  (`403 parent_permission_denied`). Si la cadena rechaza el contenido (reglas
  heredadas) responde `422 inheritance_rule_violation`. Operaciones:

  | `op` | Campos | Efecto |
  |---|---|---|
  | `add_mod` | `name` (`.jar`), `sha256`, `size`, `id?` | `mods/<name>`; si el padre lo tiene, lo reemplaza con `expect_base_object_sha256` |
  | `remove_mod` | `name` (archivo o id) o `sha256` | quita el propio y, si es heredado, añade `remove_mods` |
  | `set_config` | `path`, `sha256`, `size`, `policy?` (`default_once`) | config propia (reemplaza la heredada) |
  | `remove_config` | `path` | quita la propia y, si es heredada, añade `remove_configs` |
  | `set_config_setting` | `path`, `format`, `key`, `value`, `policy?` | regla de `config_settings` propia |
  | `remove_config_setting` | `path`, `key` | quita una regla propia |
  | `set_jvm_options` | `jvm_options`, `remove_jvm_options` | sustituye las listas propias |

  `notes` va a `revision.release_notes`. El esquema del manifiesto es el
  mínimo que admite el contenido (4 con loader, 3 con JVM, 2 con ajustes).
- **Repin** publica una revisión con el mismo contenido propio y la base en
  otra revisión del mismo padre (vieja o la última). Sirve para probar una
  actualización vieja → nueva.
- **Follow**: con `follow_parent: false` el auto-follow no re-fija ese perfil
  de prueba. Los perfiles normales siguen igual que siempre.
- Tras crear, cambiar o re-fijar, su revisión estable avanza si seguía a la
  última (como en el auto-follow) y sus propios hijos de prueba que siguen se
  re-fijan.
- **Borrar** deja la lápida de siempre: el id queda quemado
  (`409 profile_id_taken` al reutilizarlo) y sus revisiones siguen legibles por
  id.

### Catálogo

`GET /v1/profiles` oculta los `profile_test-…` salvo `?include_test=1`, para que
los jugadores no los vean. Una revisión u objeto de prueba concreto sigue siendo
legible por su id. `/v1/admin/profiles` también los oculta (el panel los lista
aparte en Servicio) y las publicaciones recientes del panel no los muestran.

### Cuotas (globales, opciones de arranque)

| Opción | Por defecto | Error al superarla |
|---|---|---|
| `--test-max-profiles` | 200 perfiles de prueba vivos | `409 quota_profiles_exceeded` |
| `--test-max-object-bytes` | 2 GiB por objeto (máximo del CAS) | `413 quota_object_too_large` |
| `--test-max-bytes` | 100 GiB | `409 quota_bytes_exceeded` |
| `--test-disk-margin-bytes` | 10 GiB libres siempre | `507 disk_space_low` |

Los bytes contados son los de objetos que **la API de prueba añadió al CAS**
(si los bytes ya estaban, p. ej. los mods del Beta, no cuentan) y que siguen
referenciados por una revisión de un perfil de prueba vivo, o que se subieron
hace menos de 24 h y aún no se han publicado. El máximo efectivo es
`min(--test-max-bytes, usados + disco libre − margen)`. El panel (Servicio) y
`GET /v1/test/usage` muestran uso, máximo y porcentaje de cada cuota y del
disco, con aviso desde el 80 %.

**Recogida de objetos.** Borrar un perfil de prueba libera su cuota al momento,
pero no borra bytes del disco: sus revisiones son inmutables y siguen legibles
por id (instalaciones existentes, otros hijos), y el CAS v1 conserva los
objetos publicados (trigger `published_objects_no_delete`). Los objetos subidos
y nunca publicados dejan de contar a las 24 h, pero su archivo sigue en el CAS.
El margen de disco es el límite real: si se acerca, el admin borra perfiles de
prueba viejos y, si hace falta espacio físico, lo trata como una tarea de
mantenimiento del CAS (fuera de este cambio). Criterio para reabrir: el disco
llega al aviso del 80 % con los perfiles de prueba ya borrados.

## Diseño de los launchers (0.4.0, contrato)

Backend del rediseño de Pandora y del launcher Android. Este apartado es el
contrato que implementan los clientes; donde lo dice, sustituye rutas y campos
anteriores. No hay compatibilidad con clientes antiguos: no se ha distribuido
ninguno.

Reglas comunes:

- **Lo cosmético es mutable y sin firma** (noticias, artwork, logo, texto del
  changelog, marca Android), como la presentación de 2026-10-01: el launcher
  confía en HTTPS. Lo que afecta a la instalación sigue en revisiones firmadas
  inmutables, con CAS SHA-256, anti-rollback y sin escaneo al arrancar; nada
  de eso cambia. La única pieza nueva con firma es el binario del launcher.
- **Lecturas públicas** (`GET`/`HEAD` sin autenticación, también fuera de la
  LAN en modo HTTPS). **Escrituras solo por `/v1/admin/…`** (LAN, sesión de
  admin y CSRF), salvo la presentación de perfiles de prueba por `/v1/test/`.
  La API de prueba no escribe noticias, changelogs ni versiones del launcher.
- **Imágenes y binarios = objetos CAS.** El panel sube los bytes con
  `POST /v1/admin/objects/sha256/{sha256}` y los referencia como
  `{sha256, size, media_type}`. El servidor comprueba hash, tamaño, tipo real
  (cabecera decodificada, no la extensión) y dimensiones antes de aceptar la
  referencia. Mientras algo los referencie se sirven en
  `GET /v1/objects/sha256/{sha256}` (inmutables); al dejar de referenciarse
  dejan de servirse, como el icono.
- **Caché del cliente:** imágenes guardadas por `sha256` y verificadas antes
  de decodificar; nunca hay que volver a bajarlas. Los documentos JSON nuevos
  y el catálogo llevan `ETag` y aceptan `If-None-Match` (`304`). Sin conexión,
  el launcher muestra lo último que guardó.
- Fechas en RFC 3339 UTC (`2026-10-19T18:30:00Z`). Límites en bytes UTF-8.
  Errores con la forma de siempre: `{"error":{"code","message"}}`.
- **Texto plano** (changelogs de revisión y de launcher): una línea en blanco
  separa párrafos; un salto simple es salto de línea; sin marcado. El cliente
  puede convertir en enlace una URL `https://` suelta. Las noticias usan
  Markdown restringido (apartado 1).

### 1. Noticias

#### Modelo

Categoría (datos del servidor, nunca constantes en código; el operador crea
las suyas en el panel):

```json
{ "id": "cat_3k9x2", "name": "Mantenimiento", "order": 20 }
```

| Campo | Regla |
|---|---|
| `id` | Lo genera el servidor (`cat_` + 5–16 `[a-z0-9]`). Estable aunque cambie el nombre. |
| `name` | 1–32 bytes, una línea, único sin distinguir mayúsculas. |
| `order` | Entero. La lista se ordena por `order` y después por `name`. |

Máximo 32 categorías. Una categoría con noticias no se borra
(`409 category_in_use`): se renombra o se mueven antes sus noticias.

Noticia:

```json
{
  "id": "news_8f2k1q0z",
  "title": "Mantenimiento del servidor el sábado",
  "body": "Primer párrafo con **negrita**.\n\n## Novedades\n\n- Uno\n- Dos\n\n![Captura](https://example.com/captura.png)\n\nhttps://youtu.be/dQw4w9WgXcQ",
  "image": { "url": "https://example.com/portada.jpg" },
  "author": "Equipo",
  "category_id": "cat_3k9x2",
  "target_profile_id": null,
  "pinned": true,
  "published_at": "2026-10-19T18:30:00Z",
  "updated_at": "2026-10-20T09:00:00Z",
  "expires_at": "2026-10-26T00:00:00Z",
  "linked_revision": { "profile_id": "profile_example", "revision_id": "rev_…" }
}
```

| Campo | Regla |
|---|---|
| `id` | Lo genera el servidor (`news_` + 8–16 `[a-z0-9]`). |
| `title` | 1–160 bytes, una línea. |
| `body` | 0–65536 bytes de **Markdown restringido** (ver «Cuerpo de la noticia»). |
| `image` | Portada. `null`, una referencia CAS subida desde el panel (`{sha256, size, media_type}`: JPEG/PNG 16:9 con tolerancia 1 %, ancho 640–3840, ≤ 4 MiB; el panel recorta y convierte a 1600 × 900 JPEG) o una imagen externa `{url}` (`https://`, ≤ 2048 bytes). La externa no la comprueba el servidor: el cliente la recorta a 16:9. |
| `author` | 1–64 bytes, una línea. Texto libre, no una cuenta. |
| `category_id` | Una categoría existente. |
| `target_profile_id` | `null` = **General**. Si no, un perfil global: la noticia es para ese perfil y **todos sus descendientes** (su familia). |
| `pinned` | Varias pueden estar fijadas a la vez. |
| `published_at` | Lo pone el servidor al crear; no se edita (no hay publicación programada). |
| `updated_at` | Última edición. |
| `expires_at` | `null` = permanente. Si es una fecha, desde ella la noticia pasa al historial. Posterior a `published_at`. |
| `linked_revision` | `null` o una revisión existente; el cliente abre su changelog (apartado 3, `?revision=`). |

#### Cuerpo de la noticia

Subconjunto de CommonMark. El servidor lo valida al guardar (`422
invalid_news_body`, con la línea en `message`) y los dos launchers deben
aceptar y pintar exactamente lo mismo: casos compartidos en
[`news-markdown-vectors.json`](news-markdown-vectors.json), que cada cliente
copia tal cual a sus tests.

| Se admite | Sintaxis |
|---|---|
| Párrafos y saltos de línea | Línea en blanco / salto simple |
| Títulos | `##` y `###` (el `#` es el titular de la noticia; `#`, `####`… se rechazan) |
| Énfasis | `**negrita**`, `*cursiva*` |
| Listas | `- ` o `* ` con viñetas, `1. ` numeradas; hasta 2 niveles |
| Cita | `> ` |
| Separador | `---` en su propia línea |
| Enlace | `[texto](https://…)` o una URL `https://` suelta |
| Imagen | `![alt](https://…)` (externa, p. ej. Imgur) o `![alt](cas:<sha256>)` (subida desde el panel; el servidor comprueba que existe) |
| Vídeo de YouTube | Un párrafo que es **solo** un enlace a YouTube: `https://www.youtube.com/watch?v=<id>`, `https://youtu.be/<id>` o `https://www.youtube.com/shorts/<id>`, con `<id>` = `[A-Za-z0-9_-]{11}` (se ignoran otros parámetros) |

Se rechaza: HTML (en bloque o en línea), tablas, bloques y spans de código,
imágenes o enlaces que no sean `https:` (o `cas:` en imágenes), títulos de
nivel 1 o 4–6, listas de más de 2 niveles, notas al pie y referencias de
enlace (`[x][1]`). Como máximo 50 imágenes y 10 vídeos por noticia; cada URL
≤ 2048 bytes.

El cliente:

- Descarga las imágenes externas sin cookies ni credenciales, solo por
  `https:` (también tras redirecciones, como mucho 3), ≤ 10 MiB y ≤ 4096 px
  por lado; si fallan, pinta el texto `alt`. Las cachea por URL.
- Pinta un vídeo como tarjeta con la miniatura
  `https://i.ytimg.com/vi/<id>/hqdefault.jpg` y un botón de reproducir que
  abre `https://www.youtube.com/watch?v=<id>` en el navegador del sistema.
  No incrusta un reproductor.
- Abre los enlaces en el navegador del sistema, nunca dentro del launcher.

Las imágenes externas y las miniaturas de YouTube hacen que el launcher
contacte con esos servidores (ven la IP del jugador). Es aceptable en una
comunidad cerrada; decisión del usuario 2026-10-23, para no gastar disco del
servidor.

Reglas para el cliente:

- **Recientes** = `expires_at` nulo o futuro. **Historial** = caducadas. Una
  caducada deja de contar como fijada aunque tenga `pinned: true`. Compara con
  `server_time` de la respuesta, no con el reloj local.
- **Destino:** una instancia ve las General (con su interruptor) y las de su
  perfil y de cada **madre** de ese perfil (`parent_profile_id`, apartado 2),
  nunca las de sus hijas. El filtro por familia usa el mismo árbol.
- «Nueva», «Guardada» y el tiempo de lectura son locales del launcher.
- Una noticia borrada en el servidor se borra también en local **aunque
  estuviera guardada**.
- Un `target_profile_id` que ya no está en el catálogo no lo ve ninguna
  instancia; el cliente no falla por ello.

#### Lectura y sincronización incremental

```text
GET /v1/news[?since=<cursor>]
```

```json
{
  "schema_version": 1,
  "server_time": "2026-10-23T10:00:00Z",
  "cursor": "n1.4821",
  "full": false,
  "categories": [ { "id": "cat_…", "name": "Actualización", "order": 10 } ],
  "items": [ { "id": "news_…", "…": "forma completa" } ],
  "deleted": [ "news_…" ]
}
```

- Sin `since`, o con un `since` desconocido o más antiguo que la retención de
  borradas: `full: true`, `items` = todas las vivas y `deleted` vacío. El
  cliente **sustituye** su caché y conserva la marca «guardada» solo de los ids
  que siguen existiendo.
- Con `since` válido: `full: false`, `items` = creadas o editadas después del
  cursor (forma completa) y `deleted` = ids borrados después del cursor.
- `categories` va siempre completa y sustituye a la local.
- `cursor` es opaco: el cliente guarda el último y lo devuelve tal cual. Las
  lápidas se guardan al menos 180 días.
- `ETag` = cursor; `If-None-Match` igual → `304`. El launcher sincroniza al
  abrirse y, como mucho, cada 5 minutos mientras está abierto.
- Sin paginación: hasta 2000 noticias vivas (`409 news_limit_reached` al crear
  más); el operador borra las viejas.

#### Panel

```text
GET    /v1/admin/news                 # todas, con caducadas
POST   /v1/admin/news                 # crear
PUT    /v1/admin/news/{id}            # editar: sustituye todos los campos editables
DELETE /v1/admin/news/{id}            # borrar (lápida para la sincronización)
GET    /v1/admin/news-categories
PUT    /v1/admin/news-categories      # sustituye la lista: [{id?, name, order}]; sin id = nueva
```

Campos editables: `title`, `body`, `image`, `author`, `category_id`,
`target_profile_id`, `pinned`, `expires_at`, `linked_revision`. Errores:
`422 invalid_news` (campo en `message`), `422 invalid_image`,
`404 news_not_found`, `409 category_in_use`.

### 2. Presentación del perfil: artwork, logo, herencia y marca Android

Sustituye a «Mutable profile presentation» en el cuerpo de la escritura y en
la forma del catálogo.

#### Escritura

```text
PUT /v1/admin/profiles/{profile_id}/presentation
PUT /v1/test/profiles/{profile_id}/presentation      # solo perfiles de prueba
```

```json
{
  "name": "Example",
  "description": "…",
  "icon":    { "sha256": "…", "size": 40211, "media_type": "image/png" },
  "artwork": { "image": { "sha256": "…", "size": 812345, "media_type": "image/jpeg" }, "author": "Autora" },
  "logo":    { "sha256": "…", "size": 91022, "media_type": "image/png" },
  "android_compatible": true
}
```

- **Campo ausente = sin cambios; `null` = quitar el valor propio** (vuelve a
  heredar). Antes el cuerpo sustituía todo; ahora es una actualización parcial.
- `icon`: como hasta ahora (PNG, ≤ 2 MiB, 1–1024 px por lado).
- `artwork.image`: JPEG o PNG de **exactamente 1920 × 760**, ≤ 6 MiB (el
  panel recorta y escala). `artwork.author`: 1–64 bytes, obligatorio.
- `logo`: PNG **con alfa** (RGBA, gris+alfa o `tRNS`), ancho 64–1600 y alto
  32–800, ≤ 2 MiB. El panel no lo recomprime.
- `android_compatible`: booleano, por defecto `false`.
- Respuesta: la presentación propia guardada.

#### Herencia

La madre de un perfil es el perfil de la `base` de su **última** revisión
(`parent_profile_id`; `null` en una raíz). El valor efectivo de `icon`,
`artwork` y `logo` es el propio si existe; si no, el efectivo de la madre,
recursivamente (hasta 8 niveles; se sigue la cadena aunque una madre esté
borrada). Para `icon`, «propio» incluye el icono firmado de la última
revisión, como hasta ahora. Así el Beta muestra icono, artwork y logo de su
madre mientras no tenga los suyos. `name`, `description` y
`android_compatible` **no** se heredan.

#### Catálogo

`GET /v1/profiles` añade a cada perfil lo siguiente y quita el subobjeto
`presentation` (los valores propios pasan a `/v1/admin/profiles` como
`own_presentation`):

```json
{
  "profile_id": "profile_example-beta",
  "name": "Example Beta",
  "parent_profile_id": "profile_example",
  "android_compatible": false,
  "icon": { "sha256": "…", "size": 40211, "media_type": "image/png" },
  "artwork": { "image": { "sha256": "…", "size": 812345, "media_type": "image/jpeg" }, "author": "Autora" },
  "logo": null,
  "inherited_from": { "icon": "profile_example", "artwork": "profile_example", "logo": null }
}
```

(más `description`, `minecraft`, `neoforge`, `loader`, `latest_revision` y
`channels` como hasta ahora). `inherited_from.<campo>` es el perfil del que
viene el valor, o `null` si es propio o no hay. Sin `logo` el launcher pinta el
nombre del perfil; sin `artwork`, el «Vanilla» que lleva dentro.

### 3. Changelog de revisiones

#### Texto

Cada revisión tiene un changelog mutable:

```json
{
  "title": "Actualización de Create",
  "description": "Por qué se hizo esta revisión, en texto plano.",
  "changes": [ "Create actualizado a 6.0.1", "Arreglado el crasheo al abrir el mapa" ]
}
```

| Campo | Regla |
|---|---|
| `title` | 0–120 bytes, una línea. |
| `description` | 0–8192 bytes de texto plano (párrafos). |
| `changes` | 0–100 cambios; cada uno 1–300 bytes, una línea, texto plano. El cliente los pinta como viñetas. |

Los mods añadidos, quitados y actualizados no se escriben aquí: van aparte en
`mods`, calculados. Si el operador nunca editó el changelog, se deriva de
`release_notes` firmado: la primera línea → `title`; las líneas que empiezan
por `- ` → `changes` (sin el guion); el resto → `description`. Las notas
automáticas `auto: sigue a …` no se muestran (ver «Origen»). La fecha es
`revision.created_at` del manifiesto firmado.

```text
PUT /v1/admin/revisions/{revision_id}/changelog      # {title, description, changes}
```

#### Origen

El origen de una revisión es la revisión que introdujo el cambio:

- Si su contenido propio (mods, configs, ajustes, opciones JVM, permisos y
  borrados) es igual al de la revisión anterior del mismo perfil **y** cambió
  la `base`, el origen es el origen de la revisión de la madre a la que ahora
  apunta (caso del auto-follow; recursivo).
- Si no, el origen es ella misma.

Se calcula sobre datos firmados: vale para todas las revisiones existentes sin
migración.

#### Lectura

```text
GET /v1/profiles/{profile_id}/changelog[?limit=50][&cursor=…]   # historial
GET /v1/profiles/{profile_id}/changelog?after=<rev>[&until=<rev>] # diálogo de actualización
GET /v1/profiles/{profile_id}/changelog?revision=<rev>           # una entrada (noticia enlazada)
```

```json
{
  "schema_version": 1,
  "profile_id": "profile_example-beta",
  "lineage": [
    { "profile_id": "profile_example-beta", "name": "Example Beta", "depth": 0 },
    { "profile_id": "profile_example",      "name": "Example",      "depth": 1 }
  ],
  "entries": [
    {
      "origin":   { "profile_id": "profile_example", "revision_id": "rev_…", "sequence": 44 },
      "revision": { "revision_id": "rev_…", "sequence": 12 },
      "created_at": "2026-10-19T18:30:00Z",
      "title": "Actualización de Create",
      "description": "…",
      "changes": [ "Create actualizado a 6.0.1" ],
      "mods": {
        "added":   [ { "id": "example-mod", "file": "example-mod-1.2.jar" } ],
        "removed": [ { "id": "old-mod", "file": "old-mod-0.9.jar" } ],
        "updated": [ { "id": "create", "from": "create-6.0.0.jar", "to": "create-6.0.1.jar" } ]
      }
    }
  ],
  "next_cursor": null
}
```

- De la más nueva a la más antigua. **Una entrada por origen**: un cambio de la
  madre que llegó por auto-follow aparece una vez, atribuido a la madre
  (`origin`), con la revisión del perfil pedido que lo trajo (`revision`).
- **Historial** (sin `after`): las revisiones del perfil y, debajo, las de sus
  madres **anteriores a la creación del perfil**, hasta la revisión que fijó
  su primera revisión (recursivo); esas entradas llevan `revision: null`.
  Nunca aparecen revisiones de hijas. `lineage` es la leyenda: perfil pedido y
  madres; el color lo elige el cliente.
- **Diálogo de actualización** (`after` = revisión local; `until` = la nueva,
  por defecto la última): solo entradas con `revision.sequence` en
  `(after, until]`. Si `after`/`until` no son del perfil → `422
  revision_not_in_profile`.
- `mods`: diferencia del conjunto efectivo de mods (cadena resuelta) entre esa
  revisión y la anterior **del perfil pedido** (con `revision: null`, del
  perfil de origen). `id` es el id del manifiesto y `file` el nombre del
  archivo. Listas vacías si no hay cambios de mods.
- `limit` 1–200 (50 por defecto); `next_cursor` lleva a la página siguiente.
- `ETag` y `304` como el resto: las revisiones no cambian, solo el texto.

### 4. Marca «compatible con Android»

`android_compatible` (apartado 2): propia de cada perfil, también de cada rama
hija; no se hereda. Se edita en «Editar perfil» del panel y sale en el
catálogo. El launcher Android, en «Desde el servidor», filtra por ella
(activado por defecto) y muestra en gris con «no Android» un perfil principal
que no la tenga. Es ayuda de catálogo, no garantía de instalación: la revisión
firmada sigue mandando. Los perfiles existentes empiezan en `false`; el
operador marca los compatibles al desplegar.

### 5. Actualizaciones del propio launcher

#### Lectura

```text
GET /v1/launcher/{app}/releases?platform=<p>[&channel=player|developer][&after=<versión>]
```

`app` ∈ `pandora`, `android`. `platform` (obligatorio), lista abierta
`[a-z0-9_-]{1,32}`; previstas `windows-x86_64`, `linux-x86_64`,
`macos-aarch64` (Pandora) y `android-universal` (Android). `channel` por
defecto `player`; `developer` ve también las de `player`.

```json
{
  "schema_version": 1,
  "app": "pandora",
  "latest": { "…": "la primera de releases, o null" },
  "releases": [
    {
      "version": "1.4.0",
      "channel": "player",
      "published_at": "2026-10-23T10:00:00Z",
      "notes": "Texto plano.",
      "artifact": {
        "platform": "windows-x86_64",
        "file_name": "example-launcher-1.4.0.exe",
        "url": "/v1/objects/sha256/<sha256>",
        "sha256": "…",
        "size": 41234567,
        "statement": "<base64url del JSON canónico>",
        "signature": { "key_id": "…", "algorithm": "Ed25519", "value": "<base64url>" }
      }
    }
  ]
}
```

- `releases`: versiones con artefacto para esa plataforma, del canal pedido y
  mayores que `after`, de la más nueva a la más antigua (para mostrar los
  changelogs de todas las versiones saltadas).
- `version`: SemVer 2.0 (`MAJOR.MINOR.PATCH[-pre]`), comparado con la
  precedencia de SemVer. Única por `app`: una vez publicada no cambia de bytes
  ni se reutiliza (`409 release_exists`).
- `url` es relativa al origen HTTPS configurado. Los binarios viven en el CAS
  (el repo de Android es privado y no se puede bajar de sus releases).

#### Firma

`statement` es el JSON canónico (RFC 8785) de

```json
{ "type": "wbombo.launcher-artifact.v1", "app": "pandora", "version": "1.4.0",
  "platform": "windows-x86_64", "sha256": "…", "size": 41234567,
  "issued_at": "2026-10-23T10:00:00Z" }
```

firmado con Ed25519 por la clave de publicación del servidor (la de
`/v1/signing-keys`; `type` impide confundirlo con un manifiesto). Antes de
instalar, el cliente:

1. Verifica la firma con una clave conocida (mismas reglas que para las
   revisiones: descubrimiento por `/v1/signing-keys` y nunca contra una clave
   fijada distinta).
2. Comprueba que `app`, `platform` y `version` del statement son los que pidió
   y que la versión es **mayor** que la instalada (nunca baja).
3. Descarga, comprueba `size` y `sha256` del statement y solo entonces
   instala. En Android el sistema exige además el mismo certificado del APK.

`notes` y `channel` no van firmados (cosméticos).

#### Panel

```text
GET    /v1/admin/launcher-releases
POST   /v1/admin/launcher-releases                    # {app, version, channel, notes, artifacts:[{platform, file_name, object:{sha256,size}}]}
PUT    /v1/admin/launcher-releases/{app}/{version}    # {channel?, notes?}
DELETE /v1/admin/launcher-releases/{app}/{version}    # retirar: deja de ofrecerse
```

El binario se sube antes por `POST /v1/admin/objects/sha256/{sha256}` (límite
del CAS, 2 GiB). Al crear, el servidor firma un statement por artefacto. Una
versión retirada no vuelve a publicarse con el mismo número; quien ya la tenga
no baja de versión.

### 6. Jugadores conectados (decisión: lado cliente)

No va en Distribution. El launcher ya hace Server List Ping: la respuesta trae
conectados, máximo y una muestra de nombres con UUID (hasta 12 en un servidor
vanilla), suficiente para las caras. Pasarlo por Distribution añadiría un
intermediario y un punto de fallo sin datos mejores. El servidor «principal»
de la instancia (tarjeta y «Entrar al servidor») es la primera entrada de su
`servers.dat`. Distribution no implementa nada para esto.
