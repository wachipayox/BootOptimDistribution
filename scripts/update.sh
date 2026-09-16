#!/usr/bin/env bash
set -euo pipefail

repo_dir="${BOOTOPTIM_DISTRIBUTION_HOME:-/opt/bootoptim-distribution}"
mode="${1:---check}"

if [[ "${EUID}" -ne 0 ]]; then
  echo "Run this updater with sudo." >&2
  exit 1
fi
if [[ ! -d "$repo_dir/.git" ]]; then
  echo "Expected a checked-out private repository at $repo_dir." >&2
  exit 1
fi
if [[ "$mode" != "--check" && "$mode" != "--apply" ]]; then
  echo "Usage: $0 --check|--apply" >&2
  exit 2
fi

cd "$repo_dir"
git fetch --quiet origin main
installed="$(cat .installed-commit 2>/dev/null || git rev-parse HEAD)"
available="$(git rev-parse origin/main)"

if [[ "$installed" == "$available" ]]; then
  echo "No update: installed and origin/main are both $installed."
  exit 0
fi
echo "Update available: installed $installed -> main $available"
if [[ "$mode" == "--check" ]]; then
  exit 0
fi

git reset --hard "$available"
"$repo_dir/scripts/install.sh"
echo "Update installed. Restart the service only after its unit has been configured."
