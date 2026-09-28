#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "Run this installer with sudo." >&2
  exit 1
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
service_name="bootoptim-distribution.service"
service_user="$(systemctl show --property=User --value "$service_name" 2>/dev/null || true)"
fragment_path="$(systemctl show --property=FragmentPath --value "$service_name" 2>/dev/null || true)"
dropin_path="/etc/systemd/system/bootoptim-distribution.service.d/50-admin-updater-binary.conf"
dropins="$(systemctl show --property=DropInPaths --value "$service_name" 2>/dev/null || true)"

if [[ -z "$service_user" || ! "$service_user" =~ ^[a-z_][a-z0-9_-]*[$]?$ ]]; then
  echo "Could not determine a valid User= for $service_name." >&2
  exit 1
fi
if [[ ! -f "$fragment_path" || "$fragment_path" != /etc/systemd/system/* ]]; then
  echo "Expected a directly managed unit file under /etc/systemd/system; got $fragment_path." >&2
  exit 1
fi
if ! systemctl cat "$service_name" >/dev/null 2>&1; then
  echo "Systemd unit $service_name is not installed." >&2
  exit 1
fi
for existing_dropin in $dropins; do
  if [[ "$existing_dropin" != "$dropin_path" ]]; then
    echo "Unexpected existing drop-in $existing_dropin; review it before installing the updater." >&2
    exit 1
  fi
done

mapfile -t commands < <(sed -n 's/^ExecStart=//p' "$fragment_path" | sed '/^[[:space:]]*$/d')
if [[ "${#commands[@]}" -ne 1 ]]; then
  echo "Expected one non-empty ExecStart= in $fragment_path." >&2
  exit 1
fi
old_command="${commands[0]}"
old_executable="${old_command%%[[:space:]]*}"
if [[ "$old_executable" != */bootoptim-distribution ]]; then
  echo "Unexpected current executable in ExecStart=: $old_executable" >&2
  exit 1
fi
if [[ ! -x /usr/local/bin/bootoptim-distribution || -L /usr/local/bin/bootoptim-distribution ]]; then
  echo "Expected the root-owned runtime binary at /usr/local/bin/bootoptim-distribution." >&2
  exit 1
fi
if [[ ! -x "$script_dir/run-admin-updater.sh" ]]; then
  echo "Updater helper not found at $script_dir/run-admin-updater.sh." >&2
  exit 1
fi

state_dir=/var/lib/bootoptim-distribution-updater
if [[ -e "$state_dir" && ( -L "$state_dir" || "$(stat -c '%u' "$state_dir")" != "0" ) ]]; then
  echo "Updater state directory must be root-owned: $state_dir" >&2
  exit 1
fi
install -d -o root -g root -m 0755 "$state_dir"
install -d -o root -g root -m 0755 /usr/local/libexec
install -d -o root -g root -m 0755 /etc/systemd/system/bootoptim-distribution.service.d

temp_dir="$(mktemp -d /run/bootoptim-update-setup.XXXXXX)"
unit_tmp="$temp_dir/bootoptim-distribution-update.service"
socket_tmp="$temp_dir/bootoptim-distribution-update.socket"
trigger_tmp="$temp_dir/bootoptim-distribution-update-trigger@.service"
dropin_tmp="$temp_dir/50-admin-updater-binary.conf"
helper_tmp="$temp_dir/bootoptim-distribution-update"
trigger_script_tmp="$temp_dir/trigger-script"
trap 'rm -f "$unit_tmp" "$socket_tmp" "$trigger_tmp" "$dropin_tmp" "$helper_tmp" "$trigger_script_tmp"; rmdir "$temp_dir"' EXIT

arguments="${old_command#"$old_executable"}"
cat >"$dropin_tmp" <<EOF
[Service]
WorkingDirectory=$state_dir
ExecStart=
ExecStart=/usr/local/bin/bootoptim-distribution$arguments
EOF

cat >"$unit_tmp" <<EOF
[Unit]
Description=Update and restart BootOptim Distribution from origin/main
Wants=network-online.target
After=network-online.target

[Service]
Type=oneshot
UMask=0027
PrivateTmp=true
ProtectSystem=full
ReadWritePaths=$state_dir /usr/local/bin /usr/local/libexec
ExecStart=/usr/bin/flock --nonblock /run/lock/bootoptim-distribution-update.lock /usr/local/libexec/bootoptim-distribution-update
ExecStartPost=/usr/bin/systemctl restart $service_name
EOF

cat >"$socket_tmp" <<EOF
[Unit]
Description=Restricted trigger for BootOptim Distribution updates

[Socket]
ListenStream=/run/bootoptim-distribution-update.sock
SocketUser=$service_user
SocketMode=0600
Accept=yes
MaxConnections=1
Service=bootoptim-distribution-update-trigger@.service

[Install]
WantedBy=sockets.target
EOF

cat >"$trigger_tmp" <<'EOF'
[Unit]
Description=Start the fixed BootOptim Distribution updater

[Service]
Type=oneshot
TimeoutStartSec=5s
StandardInput=socket
StandardOutput=socket
StandardError=journal
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ExecStart=/usr/local/libexec/bootoptim-distribution-update-trigger
EOF

cat >"$trigger_script_tmp" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

request=""
IFS= read -r -n 6 request || true
if [[ "$request" != "update" ]]; then
  printf 'rejected\n'
  exit 1
fi

if /usr/bin/systemctl --no-block start bootoptim-distribution-update.service; then
  printf 'started\n'
else
  printf 'failed\n'
  exit 1
fi
EOF

chmod 0755 "$trigger_script_tmp"
install -o root -g root -m 0755 "$script_dir/run-admin-updater.sh" "$helper_tmp"
install -o root -g root -m 0755 "$trigger_script_tmp" /usr/local/libexec/bootoptim-distribution-update-trigger
install -o root -g root -m 0755 "$helper_tmp" /usr/local/libexec/bootoptim-distribution-update
systemd-analyze verify "$unit_tmp" "$socket_tmp" "$trigger_tmp"
install -o root -g root -m 0644 "$dropin_tmp" "$dropin_path"
install -o root -g root -m 0644 "$unit_tmp" /etc/systemd/system/bootoptim-distribution-update.service
install -o root -g root -m 0644 "$socket_tmp" /etc/systemd/system/bootoptim-distribution-update.socket
install -o root -g root -m 0644 "$trigger_tmp" /etc/systemd/system/bootoptim-distribution-update-trigger@.service
systemctl daemon-reload
systemd-analyze verify "$service_name" bootoptim-distribution-update.service bootoptim-distribution-update.socket 'bootoptim-distribution-update-trigger@.service'
systemctl enable --now bootoptim-distribution-update.socket
echo "Installed the restricted update socket for $service_name (account $service_user)."
echo "The service now runs from the root-owned /usr/local/bin binary."
