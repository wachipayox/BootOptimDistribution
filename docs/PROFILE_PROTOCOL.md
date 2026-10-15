# Private profile protocol contract (v1/v2/v3)

This is the shared boundary for Distribution admin/publishing and Pandora
profile discovery/update. It is the product contract for the first service
implementation; changes must update both this document and the Pandora
integration plan. The authenticated profile API and browser publication panel
are present on `agent/integration-current`; `main` remains the deployable
release branch.

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

All state-changing admin routes require authenticated administrator session and
CSRF protection over Distribution-native HTTPS. As explicitly requested on
2026-10-01, signing is automatic in the service's persistent private data
folder. Browser and launcher never receive the private key. This supersedes
the earlier workstation-only signing policy: publication authority now follows
admin authentication and verified HTTPS server identity.

Public/client read routes:

```text
GET /v1/signing-keys  # schema_version: 1, keys: {id: base64url public key}
GET /v1/profiles
GET /v1/profiles/{profile_id}/revisions/{revision_id}
GET /v1/objects/sha256/{sha256}
```

Implemented administrator routes:

```text
GET  /v1/admin/profiles
GET  /v1/admin/profiles/{profile_id}/revisions
POST /v1/admin/objects/sha256/{sha256}                 # authenticated staged upload
POST /v1/admin/publications                           # validated request, server signs
POST /v1/admin/revisions                              # signed immutable envelope
POST /v1/admin/profiles/{profile_id}/channels/{name}/promote
POST /v1/admin/profiles/{profile_id}/channels/{name}/rollback
```

Profile creation is the publication of a valid root revision whose profile ID
does not yet exist. Child publication pins its parent in the manifest. Uploads
that have not yet been referenced are not visible as a profile/revision and
must be eligible for later orphan collection. Promotion/rollback changes only
channel state; it never mutates the published revision.

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
existing immutable revision. Channel promotion remains an explicit separate
operation; a launcher following `stable` sees the edit after promotion.

## Mutable profile presentation (2026-10-01)

`PUT /v1/admin/profiles/{profile_id}/presentation` uses the existing authenticated
admin session, LAN policy and CSRF protection. Body: `{name, description, icon}`;
icon is null or a verified PNG CAS reference (2 MiB, at most 1024x1024).
Name/description/icon are mutable catalog presentation, not game revisions.
No sequence, channel, parent pin or signed game manifest is changed, and no
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
ancestry. Deleted identifiers cannot receive publications or channel changes
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
