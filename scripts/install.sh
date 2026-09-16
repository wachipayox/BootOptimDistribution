#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
repo_dir="${BOOTOPTIM_DISTRIBUTION_HOME:-$(cd "$script_dir/.." && pwd -P)}"
bin_dir="$repo_dir/bin"
target="$bin_dir/bootoptim-distribution"
previous="$bin_dir/bootoptim-distribution.previous"

if [[ "${EUID}" -ne 0 ]]; then
  echo "Run this installer with sudo." >&2
  exit 1
fi
if [[ ! -d "$repo_dir/.git" ]]; then
  echo "Expected a checked-out private repository at $repo_dir." >&2
  exit 1
fi
command -v go >/dev/null || { echo "Install the distribution package 'golang-go' before running this script." >&2; exit 1; }

cd "$repo_dir"
commit="$(git rev-parse HEAD)"
version="$(tr -d '[:space:]' < VERSION)"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]]; then
  echo "VERSION must contain a semantic version, got: $version" >&2
  exit 1
fi
mkdir -p "$bin_dir"
staging="$(mktemp "$bin_dir/.bootoptim-distribution.XXXXXX")"

cleanup() { rm -f "$staging"; }
trap cleanup EXIT

go build -trimpath -buildvcs=false \
  -ldflags "-s -w -X main.buildVersion=$version -X main.buildCommit=$commit" \
  -o "$staging" ./cmd/bootoptim-distribution

if [[ -x "$target" ]]; then
  cp -f "$target" "$previous"
fi
install -m 0755 "$staging" "$target"
printf '%s\n' "$commit" > "$repo_dir/.installed-commit"
echo "Installed bootoptim-distribution $version ($commit)."
