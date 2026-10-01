#!/usr/bin/env bash
set -euo pipefail

fail() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

to_bash_path() {
    local path="$1"
    if [[ "$path" =~ ^[A-Za-z]:[\\/] ]] && command -v cygpath >/dev/null 2>&1; then
        cygpath -u -- "$path"
    else
        printf '%s\n' "$path"
    fi
}

if [[ $# -ne 1 ]]; then
    fail 'Arrastra un único archivo JSON de solicitud sobre sign-profile-request.cmd.'
fi

request_path="$(to_bash_path "$1")"
[[ -f "$request_path" ]] || fail "No encuentro el JSON: $1"
[[ "$request_path" == *.json ]] || fail 'El archivo de solicitud debe terminar en .json.'
request_dir="$(cd -- "$(dirname -- "$request_path")" && pwd -P)"
request_path="$request_dir/$(basename -- "$request_path")"
output_path="${request_path%.json}.signed-envelope.json"
[[ ! -e "$output_path" ]] || fail "El archivo de salida ya existe; no lo sobrescribiré: $output_path"

key_path="${BOOTOPTIM_RELEASE_SIGNING_KEY:-}"
key_path_config="${XDG_CONFIG_HOME:-${HOME:-}/.config}/bootoptim-distribution/release-key-path"
remember_key_path=false
if [[ -z "$key_path" && -f "$key_path_config" ]]; then
    IFS= read -r key_path < "$key_path_config" || true
fi
if [[ -z "$key_path" ]]; then
    printf 'Ruta del archivo de clave privada Ed25519 (.private.json): '
    IFS= read -r key_path || fail 'No se recibió la ruta de la clave.'
    remember_key_path=true
fi
key_path="$(to_bash_path "$key_path")"
[[ -f "$key_path" ]] || fail "No encuentro la clave privada: $key_path"
key_dir="$(cd -- "$(dirname -- "$key_path")" && pwd -P)"
key_path="$key_dir/$(basename -- "$key_path")"
if [[ "$remember_key_path" == true ]]; then
    mkdir -p -- "$(dirname -- "$key_path_config")"
    (umask 077; printf '%s\n' "$key_path" > "$key_path_config")
fi

command -v go >/dev/null 2>&1 || fail 'Go no está en PATH. Instala Go o ejecútalo desde una consola que lo encuentre.'
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd -- "$script_dir/.." && pwd -P)"
[[ -f "$repo_root/go.mod" && -f "$repo_root/cmd/bootoptim-release-signer/main.go" ]] \
    || fail 'El script debe permanecer dentro del repositorio BootOptimDistribution.'

printf '\nFirmando solicitud:\n  %s\nEl sobre firmado se guardará junto al JSON original.\n\n' "$request_path"
cd -- "$repo_root"
go run ./cmd/bootoptim-release-signer sign \
    --key "$key_path" \
    --request "$request_path" \
    --out "$output_path"

printf '\nListo. Archivo firmado:\n%s\n' "$output_path"
