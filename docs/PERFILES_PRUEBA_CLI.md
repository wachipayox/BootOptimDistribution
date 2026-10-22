# CLI de perfiles de prueba (`tools/perfiles_prueba.py`)

Herramienta de terminal para que los agentes creen, cambien, publiquen,
consulten y borren perfiles **de prueba** (`profile_test-…`) en Distribution.
Los perfiles normales (Principal, Beta…) **no** se pueden modificar con ella:
el CLI lo rechaza antes de llamar y el servidor también (`403
not_a_test_profile`). Contrato del servidor: sección «Perfiles de prueba» de
[`PROFILE_PROTOCOL.md`](PROFILE_PROTOCOL.md).

Python 3.8+, solo biblioteca estándar; funciona igual en Windows y Linux.

## Configuración

| Variable | Para qué |
|---|---|
| `WBOMBO_DIST_URL` | URL base `https://…` del servicio. Obligatoria. |
| `WBOMBO_DIST_TOKEN` | Token compartido de la API de prueba. Solo para escribir y para `usage`. |
| `WBOMBO_AGENT` | Etiqueta del dueño (p. ej. `agente-269`). Se guarda en el perfil; es informativa. |
| `WBOMBO_DIST_CA_FILE` | Opcional: PEM de la CA del certificado del servidor si no es de una CA pública. |

El token y la URL son secretos del entorno (secretos de Claude Code/Codex o de
GitHub Actions). Nunca van en un repo, un prompt ni un log. El CLI **nunca**
imprime ni la URL ni el token. Leer (`list`, `show`, `diff`) no necesita token.

El token lo genera el admin en el panel: **Servicio → Perfiles de prueba →
Generar token** (o **Rotar token**, que invalida el anterior al momento).

## Salida y códigos

La primera línea siempre es estable y se puede filtrar:

```text
[WBOMBO-TESTPROFILE] <ESTADO> profile=<id> revision=<rev> manifest_sha256=<sha>
```

Los campos que no aplican salen como `-`. Los mensajes de progreso (subidas)
y los detalles van después; los errores, a stderr.

| Salida | Estado | Cuándo |
|---|---|---|
| 0 | `OK` | hecho |
| 1 | `ERROR` | red, servidor (5xx) u otro fallo |
| 2 | `USAGE` | argumentos o petición inválidos (HTTP 400), falta `WBOMBO_DIST_URL` |
| 3 | `REJECTED` | el id no es un perfil de prueba |
| 4 | `AUTH` | sin token, token incorrecto o rotado (401), demasiados fallos (429), token aún no generado (503) |
| 5 | `NOT_FOUND` | perfil, revisión o padre inexistente, o perfil de prueba borrado |
| 6 | `CONFLICT` | conflicto (409), validación (422), permisos del padre, cuotas (409/413/507) |

## Subcomandos

```bash
python tools/perfiles_prueba.py list [--include-test]
python tools/perfiles_prueba.py show <id> [--rev <rev>] [--effective] [--json]
python tools/perfiles_prueba.py diff <id> <revA> <revB>
python tools/perfiles_prueba.py create <slug> --parent <id> [--parent-rev <rev>|latest] [--pinned] [--name "<nombre>"]
python tools/perfiles_prueba.py add-mod <id> <jar>… [--notes "…"]
python tools/perfiles_prueba.py remove-mod <id> <nombre|sha256>… [--notes "…"]
python tools/perfiles_prueba.py set-config <id> <ruta> <archivo> [--policy default_once|enforced] [--notes "…"]
python tools/perfiles_prueba.py apply <id> <cambios.json> [--notes "…"]
python tools/perfiles_prueba.py repin <id> [--parent-rev <rev>|latest]
python tools/perfiles_prueba.py follow <id> on|off
python tools/perfiles_prueba.py delete <id> --confirm <id>
python tools/perfiles_prueba.py usage
```

- `create`: el id es `profile_test-<slug>` (`[a-z0-9_-]`, hasta 59
  caracteres). Por defecto sigue al padre; `--pinned` lo crea con
  `follow_parent: false`.
- `add-mod`: calcula el SHA-256, pregunta con `HEAD` si el servidor ya tiene
  los bytes y solo sube lo que falta. Si el padre ya tiene ese archivo
  (`mods/<nombre>`), lo reemplaza. Todos los jars van en una sola revisión.
- `remove-mod`: por nombre de archivo, id de mod o SHA-256 del efectivo.
- `set-config`: `<ruta>` es la ruta dentro de la instancia (p. ej.
  `config/create-common.toml`).
- `apply`: varias operaciones en una revisión. Formato:

  ```json
  {
    "notes": "probar Create 6.1 con su config",
    "operations": [
      {"op": "add_mod", "file": "jars/create-6.1.jar"},
      {"op": "remove_mod", "name": "create-6.0.jar"},
      {"op": "set_config", "path": "config/create-common.toml", "file": "create-common.toml", "policy": "enforced"},
      {"op": "set_config_setting", "path": "config/create-common.toml", "format": "toml", "key": "kinetics.maxBeltLength", "value": 40},
      {"op": "set_jvm_options", "jvm_options": [], "remove_jvm_options": []}
    ]
  }
  ```

  `file` (relativo al JSON) se sube si falta y se sustituye por `sha256` y
  `size`. Las operaciones están en `PROFILE_PROTOCOL.md`.
- `show --effective` resuelve la herencia y lista los mods efectivos;
  `diff` compara los archivos efectivos de dos revisiones.
- `delete` deja una lápida: el id queda quemado y sus revisiones siguen
  legibles por id.

## Recetas

**Hijo de prueba del Beta para validar** (sigue al Beta):

```bash
python tools/perfiles_prueba.py create 269-cli-smoke --parent profile_wachiland-elite-beta
python tools/perfiles_prueba.py add-mod profile_test-269-cli-smoke build/libs/candidato.jar --notes "candidato BootOptim abc123"
```

**Serie de medición** (que el auto-follow no la mueva):

```bash
python tools/perfiles_prueba.py create 270-medicion --parent profile_wachiland-elite-beta --pinned
```

**Probar una actualización vieja → nueva:**

```bash
python tools/perfiles_prueba.py create 271-update --parent profile_wachiland-elite-beta --parent-rev rev_<vieja> --pinned
# instalar la instancia con esa revisión, luego:
python tools/perfiles_prueba.py repin profile_test-271-update --parent-rev latest
# actualizar la instancia y comprobar
```

**Al terminar**, bórralo y mira las cuotas:

```bash
python tools/perfiles_prueba.py delete profile_test-269-cli-smoke --confirm profile_test-269-cli-smoke
python tools/perfiles_prueba.py usage
```
