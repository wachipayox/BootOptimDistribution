#!/usr/bin/env python3
"""Perfiles de prueba de BootOptim Distribution desde la terminal.

Solo biblioteca estándar; funciona en Windows y Linux (Python 3.8+).

Entorno:
  WBOMBO_DIST_URL      URL base del servicio (https://…). Nunca se imprime.
  WBOMBO_DIST_TOKEN    token compartido de la API de prueba. Nunca se imprime.
  WBOMBO_AGENT         etiqueta del dueño (p. ej. agente-269). Informativa.
  WBOMBO_DIST_CA_FILE  opcional: PEM de la CA que firmó el certificado del servidor.

La primera línea de salida es estable y se puede filtrar:
  [WBOMBO-TESTPROFILE] <ESTADO> profile=<id> revision=<rev> manifest_sha256=<sha>

Códigos de salida: 0 ok · 1 error · 2 uso · 3 no es un perfil de prueba ·
4 autenticación · 5 no existe · 6 conflicto, validación o cuota.

Guía completa: docs/PERFILES_PRUEBA_CLI.md
"""

import argparse
import hashlib
import json
import os
import re
import ssl
import sys
import urllib.error
import urllib.parse
import urllib.request

PREFIX = "[WBOMBO-TESTPROFILE]"
TEST_PREFIX = "profile_test-"
DIGEST_RE = re.compile(r"^[0-9a-f]{64}$")

EXIT_OK, EXIT_ERROR, EXIT_USAGE, EXIT_NOT_TEST, EXIT_AUTH, EXIT_NOT_FOUND, EXIT_CONFLICT = 0, 1, 2, 3, 4, 5, 6
STATUS_WORD = {
    EXIT_OK: "OK", EXIT_ERROR: "ERROR", EXIT_USAGE: "USAGE", EXIT_NOT_TEST: "REJECTED",
    EXIT_AUTH: "AUTH", EXIT_NOT_FOUND: "NOT_FOUND", EXIT_CONFLICT: "CONFLICT",
}


class CliError(Exception):
    def __init__(self, code, message, profile="-", revision="-", sha="-"):
        super().__init__(message)
        self.code, self.message = code, message
        self.profile, self.revision, self.sha = profile, revision, sha


def first_line(code, profile="-", revision="-", sha="-"):
    print("%s %s profile=%s revision=%s manifest_sha256=%s" % (PREFIX, STATUS_WORD[code], profile or "-", revision or "-", sha or "-"))


def exit_for(status, error_code):
    if status == 400:
        return EXIT_USAGE
    if status in (401, 429) or error_code in ("test_token_not_configured", "invalid_token", "too_many_auth_failures", "invalid_agent"):
        return EXIT_AUTH
    if status == 403 and error_code in ("not_a_test_profile", "test_profile_admin_write"):
        return EXIT_NOT_TEST
    if status == 404:
        return EXIT_NOT_FOUND
    if status in (403, 409, 413, 422, 507):
        return EXIT_CONFLICT
    return EXIT_ERROR


class Client:
    def __init__(self, need_token):
        self.base = os.environ.get("WBOMBO_DIST_URL", "").strip().rstrip("/")
        self.token = os.environ.get("WBOMBO_DIST_TOKEN", "").strip()
        self.agent = os.environ.get("WBOMBO_AGENT", "").strip()
        if not self.base:
            raise CliError(EXIT_USAGE, "falta WBOMBO_DIST_URL en el entorno")
        if not self.base.lower().startswith("https://"):
            raise CliError(EXIT_USAGE, "WBOMBO_DIST_URL debe ser https:// (el token no viaja en claro)")
        if need_token and not self.token:
            raise CliError(EXIT_AUTH, "falta WBOMBO_DIST_TOKEN en el entorno")
        cafile = os.environ.get("WBOMBO_DIST_CA_FILE", "").strip() or None
        self.context = ssl.create_default_context(cafile=cafile)

    def request(self, method, path, body=None, auth=True, data=None, length=None, raw=False):
        headers = {"Accept": "application/json"}
        if auth and self.token:
            headers["Authorization"] = "Bearer " + self.token
            if self.agent:
                headers["X-Wbombo-Agent"] = self.agent
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        if length is not None:
            headers["Content-Length"] = str(length)
            headers["Content-Type"] = "application/octet-stream"
        req = urllib.request.Request(self.base + path, data=data, method=method, headers=headers)
        try:
            with urllib.request.urlopen(req, context=self.context, timeout=600) as resp:
                payload = resp.read()
                return resp.status, (payload if raw else _json(payload)), resp.headers
        except urllib.error.HTTPError as err:
            payload = err.read()
            return err.code, _json(payload), err.headers
        except urllib.error.URLError as err:
            # The reason never contains the URL; keep it that way.
            raise CliError(EXIT_ERROR, "no se pudo conectar con el servicio: %s" % _safe_reason(err.reason))
        except (OSError, ValueError) as err:
            raise CliError(EXIT_ERROR, "fallo de red o de URL: %s" % err.__class__.__name__)

    def call(self, method, path, body=None, auth=True, ok=(200, 201), profile="-"):
        status, payload, _ = self.request(method, path, body=body, auth=auth)
        if status in ok:
            return payload
        raise api_error(status, payload, profile)


def _safe_reason(reason):
    text = getattr(reason, "strerror", None) or reason.__class__.__name__
    return str(text)


def _json(payload):
    if not payload:
        return {}
    try:
        return json.loads(payload.decode("utf-8"))
    except ValueError:
        return {}


def api_error(status, payload, profile="-"):
    error = payload.get("error") if isinstance(payload, dict) else None
    code = error.get("code", "") if isinstance(error, dict) else ""
    message = error.get("message", "") if isinstance(error, dict) else ""
    return CliError(exit_for(status, code), "HTTP %d %s: %s" % (status, code or "-", message or "sin detalle"), profile=profile)


def q(value):
    return urllib.parse.quote(value, safe="")


# --------------------------------------------------------------------------
# Reading (public, no token needed)

def catalog(client, include_test):
    path = "/v1/profiles" + ("?include_test=1" if include_test else "")
    return client.call("GET", path, auth=False).get("profiles", [])


def latest_ref(client, profile_id):
    for profile in catalog(client, include_test=True):
        if profile.get("profile_id") == profile_id:
            return profile
    raise CliError(EXIT_NOT_FOUND, "el perfil no existe o fue borrado", profile=profile_id)


def fetch_manifest(client, profile_id, revision_id):
    payload = client.call("GET", "/v1/profiles/%s/revisions/%s" % (q(profile_id), q(revision_id)), auth=False, profile=profile_id)
    envelope = payload.get("envelope", {})
    manifest = envelope.get("manifest")
    if isinstance(manifest, str):
        manifest = json.loads(manifest)
    return manifest, envelope.get("manifest_sha256", "-")


def effective(client, profile_id, revision_id, depth=0):
    """Resolved file tree by path, following the pinned parents."""
    if depth > 8:
        raise CliError(EXIT_ERROR, "la cadena de herencia supera 8 niveles", profile=profile_id)
    manifest, _ = fetch_manifest(client, profile_id, revision_id)
    files = {}
    base = manifest.get("base")
    if base:
        files = effective(client, base["profile_id"], base["revision_id"], depth + 1)
    for removal in manifest.get("remove_mods") or []:
        for path, entry in list(files.items()):
            if entry["kind"] == "mod" and entry["id"] == removal["id"]:
                del files[path]
                break
    for removal in manifest.get("remove_configs") or []:
        files.pop(removal["path"], None)
    for kind, key in (("mod", "mods"), ("config", "configs"), ("object", "objects")):
        for entry in manifest.get(key) or []:
            files[entry["path"]] = {"kind": kind, "id": entry.get("id", ""), "sha256": entry["object"]["sha256"], "size": entry["object"]["size"]}
    return files


def human_size(value):
    value = float(value or 0)
    for unit in ("B", "KiB", "MiB", "GiB", "TiB"):
        if value < 1024 or unit == "TiB":
            return ("%.0f %s" if unit == "B" else "%.1f %s") % (value, unit)
        value /= 1024


def cmd_list(client, args):
    profiles = catalog(client, args.include_test)
    owners = {}
    if args.include_test and client.token:
        status, payload, _ = client.request("GET", "/v1/test/profiles")
        if status == 200:
            owners = {p["profile_id"]: p for p in payload.get("profiles", [])}
    first_line(EXIT_OK)
    for profile in profiles:
        latest = profile.get("latest_revision", {})
        stable = next((c for c in profile.get("channels", []) if c.get("name") == "stable"), None)
        line = "%s  rev=%s seq=%s estable=%s  %s" % (
            profile["profile_id"], latest.get("revision_id", "-"), latest.get("sequence", "-"),
            stable.get("revision_id", "-") if stable else "-", profile.get("name", ""))
        extra = owners.get(profile["profile_id"])
        if extra:
            line += "  [dueño=%s sigue_padre=%s]" % (extra.get("owner"), "sí" if extra.get("follow_parent") else "no")
        print(line)
    return EXIT_OK


def cmd_show(client, args):
    revision = args.rev or latest_ref(client, args.id)["latest_revision"]["revision_id"]
    manifest, sha = fetch_manifest(client, args.id, revision)
    first_line(EXIT_OK, args.id, revision, sha)
    if args.json:
        print(json.dumps(manifest, indent=2, ensure_ascii=False))
        return EXIT_OK
    rev = manifest.get("revision", {})
    game = manifest.get("game", {})
    loader = game.get("loader") or {"kind": "neoforge", "version": game.get("neoforge", "-")}
    print("nombre: %s" % manifest.get("profile", {}).get("name", ""))
    print("secuencia: %s · creada: %s" % (rev.get("sequence"), rev.get("created_at")))
    print("notas: %s" % (rev.get("release_notes") or "-"))
    print("juego: Minecraft %s · %s %s" % (game.get("minecraft"), loader.get("kind"), loader.get("version")))
    base = manifest.get("base")
    print("padre: %s" % ("%s rev %s" % (base["profile_id"], base["revision_id"]) if base else "ninguno (raíz)"))
    for entry in manifest.get("mods") or []:
        print("  + mod %s (%s, %s)" % (entry["path"], entry["object"]["sha256"][:12], human_size(entry["object"]["size"])))
    for entry in manifest.get("remove_mods") or []:
        print("  - mod %s" % entry["id"])
    for entry in manifest.get("configs") or []:
        print("  + config %s (%s)" % (entry["path"], entry.get("policy")))
    for entry in manifest.get("remove_configs") or []:
        print("  - config %s" % entry["path"])
    for entry in manifest.get("config_settings") or []:
        print("  = ajuste %s %s=%s (%s)" % (entry["path"], entry["key"], json.dumps(entry["value"]), entry["policy"]))
    for entry in manifest.get("jvm_options") or []:
        print("  = jvm %s" % json.dumps(entry, ensure_ascii=False))
    if args.effective:
        files = effective(client, args.id, revision)
        mods = sorted(p for p, f in files.items() if f["kind"] == "mod")
        print("efectivo: %d archivos, %d mods" % (len(files), len(mods)))
        for path in mods:
            print("  %s" % path)
    return EXIT_OK


def cmd_diff(client, args):
    left = effective(client, args.id, args.rev_a)
    right = effective(client, args.id, args.rev_b)
    _, sha = fetch_manifest(client, args.id, args.rev_b)
    first_line(EXIT_OK, args.id, args.rev_b, sha)
    changes = 0
    for path in sorted(set(left) | set(right)):
        a, b = left.get(path), right.get(path)
        if a and not b:
            print("- %s %s" % (a["kind"], path))
        elif b and not a:
            print("+ %s %s" % (b["kind"], path))
        elif a["sha256"] != b["sha256"]:
            print("~ %s %s (%s → %s)" % (b["kind"], path, a["sha256"][:12], b["sha256"][:12]))
        else:
            continue
        changes += 1
    print("%d diferencias de archivos entre %s y %s" % (changes, args.rev_a, args.rev_b))
    return EXIT_OK


# --------------------------------------------------------------------------
# Writing (token required)

def require_test_id(profile_id):
    if not profile_id.startswith(TEST_PREFIX):
        raise CliError(EXIT_NOT_TEST, "%s no es un perfil de prueba: la terminal solo escribe profile_test-…" % profile_id, profile=profile_id)


def file_digest(path):
    digest = hashlib.sha256()
    size = 0
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
            size += len(chunk)
    return digest.hexdigest(), size


def upload(client, path, profile="-"):
    """Uploads a local file to CAS only if HEAD says it is missing."""
    if not os.path.isfile(path):
        raise CliError(EXIT_USAGE, "no existe el archivo %s" % path, profile=profile)
    sha, size = file_digest(path)
    status, _, _ = client.request("HEAD", "/v1/test/objects/sha256/" + sha)
    if status == 200:
        print("objeto %s ya estaba en el servidor (%s)" % (sha[:12], human_size(size)))
        return sha, size
    if status != 404:
        raise api_error(status, {}, profile)
    with open(path, "rb") as handle:
        status, payload, _ = client.request("POST", "/v1/test/objects/sha256/" + sha, data=handle, length=size)
    if status != 200:
        raise api_error(status, payload, profile)
    print("objeto %s subido (%s)" % (sha[:12], human_size(size)))
    return sha, size


def publish_change(client, profile_id, operations, notes):
    body = {"operations": operations}
    if notes:
        body["notes"] = notes
    return client.call("POST", "/v1/test/profiles/%s/changes" % q(profile_id), body=body, profile=profile_id)


def report_published(payload):
    rev = payload.get("revision", {})
    first_line(EXIT_OK, payload.get("profile_id"), rev.get("revision_id"), rev.get("manifest_sha256"))
    print("secuencia %s · sigue al padre: %s · estable: %s" % (
        rev.get("sequence"), "sí" if payload.get("follow_parent") else "no", payload.get("stable") or "-"))
    if payload.get("stable_message"):
        print("aviso estable: %s" % payload["stable_message"])
    for item in payload.get("auto_follow") or []:
        target = item.get("revision") or {}
        print("auto-follow %s: %s %s" % (item.get("profile_id"), item.get("status"), target.get("revision_id") or item.get("code", "")))
    return EXIT_OK


def cmd_create(client, args):
    slug = args.slug[len(TEST_PREFIX):] if args.slug.startswith(TEST_PREFIX) else args.slug
    body = {"slug": slug, "parent_profile_id": args.parent, "parent_revision_id": args.parent_rev, "follow_parent": not args.pinned}
    if args.name:
        body["name"] = args.name
    return report_published(client.call("POST", "/v1/test/profiles", body=body, profile=TEST_PREFIX + slug))


def cmd_add_mod(client, args):
    require_test_id(args.id)
    operations = []
    for jar in args.jars:
        sha, size = upload(client, jar, args.id)
        operations.append({"op": "add_mod", "name": os.path.basename(jar), "sha256": sha, "size": size})
    return report_published(publish_change(client, args.id, operations, args.notes))


def cmd_remove_mod(client, args):
    require_test_id(args.id)
    operations = []
    for target in args.mods:
        operations.append({"op": "remove_mod", "sha256": target} if DIGEST_RE.match(target) else {"op": "remove_mod", "name": target})
    return report_published(publish_change(client, args.id, operations, args.notes))


def cmd_set_config(client, args):
    require_test_id(args.id)
    sha, size = upload(client, args.file, args.id)
    op = {"op": "set_config", "path": args.path, "sha256": sha, "size": size, "policy": args.policy}
    return report_published(publish_change(client, args.id, [op], args.notes))


def cmd_apply(client, args):
    require_test_id(args.id)
    try:
        with open(args.changes, "r", encoding="utf-8") as handle:
            change = json.load(handle)
    except (OSError, ValueError) as err:
        raise CliError(EXIT_USAGE, "no se pudo leer %s: %s" % (args.changes, err), profile=args.id)
    operations = change.get("operations") if isinstance(change, dict) else None
    if not isinstance(operations, list):
        raise CliError(EXIT_USAGE, 'el archivo debe ser {"notes": "...", "operations": [...]}', profile=args.id)
    base_dir = os.path.dirname(os.path.abspath(args.changes))
    for op in operations:
        local = op.pop("file", None)
        if local:
            local = local if os.path.isabs(local) else os.path.join(base_dir, local)
            op["sha256"], op["size"] = upload(client, local, args.id)
            if op.get("op") == "add_mod" and not op.get("name"):
                op["name"] = os.path.basename(local)
    return report_published(publish_change(client, args.id, operations, args.notes or change.get("notes", "")))


def cmd_repin(client, args):
    require_test_id(args.id)
    payload = client.call("POST", "/v1/test/profiles/%s/repin" % q(args.id), body={"parent_revision_id": args.parent_rev}, profile=args.id)
    return report_published(payload)


def cmd_follow(client, args):
    require_test_id(args.id)
    payload = client.call("PUT", "/v1/test/profiles/%s/follow" % q(args.id), body={"follow_parent": args.state == "on"}, profile=args.id)
    first_line(EXIT_OK, args.id)
    print("sigue al padre: %s" % ("sí" if payload.get("follow_parent") else "no"))
    return EXIT_OK


def cmd_delete(client, args):
    require_test_id(args.id)
    if args.confirm != args.id:
        raise CliError(EXIT_USAGE, "para borrar escribe el id exacto: --confirm %s" % args.id, profile=args.id)
    client.call("DELETE", "/v1/test/profiles/%s" % q(args.id), body={"confirm_profile_id": args.confirm}, profile=args.id)
    first_line(EXIT_OK, args.id)
    print("borrado: lápida creada; el id queda quemado y sus revisiones siguen legibles por id")
    return EXIT_OK


def bar(percent, width=30):
    filled = max(0, min(width, int(round(percent * width / 100.0))))
    return "[" + "#" * filled + "." * (width - filled) + "]"


def cmd_usage(client, args):
    usage = client.call("GET", "/v1/test/usage")
    first_line(EXIT_OK)
    profiles, data, disk = usage["profiles"], usage["bytes"], usage["disk"]
    print("perfiles de prueba %s %d / %d (%.0f %%)%s" % (bar(profiles["percent"]), profiles["used"], profiles["max"], profiles["percent"], "  AVISO" if profiles["warning"] else ""))
    print("bytes de prueba    %s %s / %s (%.0f %%)%s" % (bar(data["percent"]), human_size(data["used"]), human_size(data["max"]), data["percent"], "  AVISO" if data["warning"] else ""))
    print("máximo efectivo    %s (cuota limitada por disco libre − margen)" % human_size(usage["effective_max_bytes"]))
    print("objeto máximo      %s" % human_size(usage["object_max_bytes"]))
    if disk.get("available"):
        print("disco              %s %s libres de %s (%.0f %% usado, margen %s)%s" % (
            bar(disk["used_percent"]), human_size(disk["free_bytes"]), human_size(disk["total_bytes"]),
            disk["used_percent"], human_size(disk["margin_bytes"]), "  AVISO" if disk["warning"] else ""))
    for warning in usage.get("warnings") or []:
        print("aviso: %s" % warning)
    return EXIT_OK


# --------------------------------------------------------------------------

class Parser(argparse.ArgumentParser):
    def error(self, message):
        first_line(EXIT_USAGE)
        self.print_usage(sys.stderr)
        sys.stderr.write("error: %s\n" % message)
        sys.exit(EXIT_USAGE)


def build_parser():
    parser = Parser(prog="perfiles_prueba.py", description="Perfiles de prueba de Distribution (solo profile_test-…).")
    sub = parser.add_subparsers(dest="command", parser_class=Parser)
    sub.required = True

    p = sub.add_parser("list", help="catálogo; con --include-test también los de prueba")
    p.add_argument("--include-test", action="store_true")
    p.set_defaults(func=cmd_list, token=False)

    p = sub.add_parser("show", help="una revisión (la última si no se indica)")
    p.add_argument("id")
    p.add_argument("--rev")
    p.add_argument("--effective", action="store_true", help="lista también los mods efectivos con la herencia")
    p.add_argument("--json", action="store_true", help="imprime el manifiesto firmado completo")
    p.set_defaults(func=cmd_show, token=False)

    p = sub.add_parser("diff", help="diferencias de archivos efectivos entre dos revisiones")
    p.add_argument("id")
    p.add_argument("rev_a")
    p.add_argument("rev_b")
    p.set_defaults(func=cmd_diff, token=False)

    p = sub.add_parser("create", help="crea profile_test-<slug>, hijo de --parent")
    p.add_argument("slug")
    p.add_argument("--parent", required=True)
    p.add_argument("--parent-rev", default="latest")
    p.add_argument("--pinned", action="store_true", help="no seguir al padre automáticamente")
    p.add_argument("--name")
    p.set_defaults(func=cmd_create, token=True)

    p = sub.add_parser("add-mod", help="añade o reemplaza mods (sube solo lo que falta)")
    p.add_argument("id")
    p.add_argument("jars", nargs="+")
    p.add_argument("--notes")
    p.set_defaults(func=cmd_add_mod, token=True)

    p = sub.add_parser("remove-mod", help="quita mods por nombre de archivo o sha256")
    p.add_argument("id")
    p.add_argument("mods", nargs="+")
    p.add_argument("--notes")
    p.set_defaults(func=cmd_remove_mod, token=True)

    p = sub.add_parser("set-config", help="pone una config (ruta dentro de la instancia)")
    p.add_argument("id")
    p.add_argument("path")
    p.add_argument("file")
    p.add_argument("--policy", choices=["default_once", "enforced"], default="default_once")
    p.add_argument("--notes")
    p.set_defaults(func=cmd_set_config, token=True)

    p = sub.add_parser("apply", help="varias operaciones en una revisión desde un JSON")
    p.add_argument("id")
    p.add_argument("changes")
    p.add_argument("--notes")
    p.set_defaults(func=cmd_apply, token=True)

    p = sub.add_parser("repin", help="re-fija el padre a otra revisión (o la última)")
    p.add_argument("id")
    p.add_argument("--parent-rev", default="latest")
    p.set_defaults(func=cmd_repin, token=True)

    p = sub.add_parser("follow", help="activa o desactiva el seguimiento automático del padre")
    p.add_argument("id")
    p.add_argument("state", choices=["on", "off"])
    p.set_defaults(func=cmd_follow, token=True)

    p = sub.add_parser("delete", help="borra (lápida) un perfil de prueba")
    p.add_argument("id")
    p.add_argument("--confirm", required=True)
    p.set_defaults(func=cmd_delete, token=True)

    p = sub.add_parser("usage", help="cuotas de la API de prueba y disco")
    p.set_defaults(func=cmd_usage, token=True)
    return parser


def main(argv=None):
    args = build_parser().parse_args(argv)
    try:
        client = Client(need_token=args.token)
        return args.func(client, args)
    except CliError as err:
        first_line(err.code, err.profile, err.revision, err.sha)
        sys.stderr.write("error: %s\n" % err.message)
        return err.code


if __name__ == "__main__":
    try:
        sys.stdout.reconfigure(encoding="utf-8")
        sys.stderr.reconfigure(encoding="utf-8")
    except AttributeError:
        pass
    sys.exit(main())
