#!/usr/bin/env bash
set -euo pipefail

readonly repository="https://github.com/wachipayox/BootOptimDistribution.git"
readonly state_dir="/var/lib/bootoptim-distribution-updater"
readonly runtime_binary="/usr/local/bin/bootoptim-distribution"

export PATH="/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
export HOME=/root
export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_TERMINAL_PROMPT=0

if [[ ! -d "$state_dir" || -L "$state_dir" || "$(stat -c '%u' "$state_dir")" != "0" ]]; then
  echo "Updater state directory must be a real root-owned directory: $state_dir" >&2
  exit 1
fi
if [[ ! -x "$runtime_binary" || -L "$runtime_binary" ]]; then
  echo "Runtime binary must exist as a regular executable: $runtime_binary" >&2
  exit 1
fi
command -v git >/dev/null || { echo "git is required to update Distribution." >&2; exit 1; }
command -v go >/dev/null || { echo "Go is required to build Distribution." >&2; exit 1; }

work_dir="$(mktemp -d "$state_dir/build.XXXXXX")"
case "$work_dir" in
  "$state_dir"/build.*) ;;
  *) echo "Unexpected updater staging path." >&2; exit 1 ;;
esac
cleanup() { rm -rf -- "$work_dir"; }
trap cleanup EXIT

git -c core.hooksPath=/dev/null clone --quiet --depth 1 --single-branch --branch main "$repository" "$work_dir/source"
source_dir="$work_dir/source"
commit="$(git -C "$source_dir" rev-parse HEAD)"
version="$(tr -d '[:space:]' < "$source_dir/VERSION")"
if [[ ! "$commit" =~ ^[a-f0-9]{40}$ || ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]]; then
  echo "The main checkout has an invalid commit or service version." >&2
  exit 1
fi

(cd "$source_dir" && go build -trimpath -buildvcs=false \
	-ldflags "-s -w -X main.buildVersion=$version -X main.buildCommit=$commit" \
	-o "$work_dir/bootoptim-distribution" ./cmd/bootoptim-distribution)

# Refresh the root-owned helper and its systemd units from this trusted main
# checkout before replacing the running service binary. Fail closed if that
# configuration cannot be verified.
bash "$source_dir/scripts/install-admin-updater.sh"

if [[ -x "$runtime_binary" ]]; then
  cp -p "$runtime_binary" "$runtime_binary.previous"
fi
install -o root -g root -m 0755 "$work_dir/bootoptim-distribution" "$runtime_binary.new"
mv -f "$runtime_binary.new" "$runtime_binary"

printf '%s\n' "$commit" > "$state_dir/installed-commit.new"
chmod 0644 "$state_dir/installed-commit.new"
chown root:root "$state_dir/installed-commit.new"
mv -f "$state_dir/installed-commit.new" "$state_dir/installed-commit"

logger -t bootoptim-distribution-update "Installed Distribution $version ($commit) from main."
printf 'Installed Distribution %s (%s).\n' "$version" "$commit"
