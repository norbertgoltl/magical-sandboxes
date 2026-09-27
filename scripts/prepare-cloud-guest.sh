#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
source "$ROOT/dependencies.lock"
BASE_DIR="$ROOT/.msbx-dev/guest/debian-13-generic-arm64-$DEBIAN_IMAGE_VERSION"
SANDBOX_DIR="$ROOT/.msbx-dev/sandboxes/template"
ARCHIVE="$BASE_DIR/debian-13-generic-arm64-$DEBIAN_IMAGE_VERSION.tar.xz"
BASE_DISK="$BASE_DIR/disk.raw"
DISK="$SANDBOX_DIR/disk.raw"
SEED_DIR="$SANDBOX_DIR/seed"
SEED_ISO="$SANDBOX_DIR/seed.iso"
EFI_STORE="$SANDBOX_DIR/efi-vars.fd"
AGENT="$ROOT/bin/msbx-agent-linux-arm64"
HOST_UID="$(id -u)"
BASE_URL="https://cloud.debian.org/images/cloud/trixie/$DEBIAN_IMAGE_VERSION"
ARCHIVE_NAME="debian-13-generic-arm64-$DEBIAN_IMAGE_VERSION.tar.xz"

mkdir -p "$BASE_DIR" "$SANDBOX_DIR" "$SEED_DIR"
rm -f "$SANDBOX_DIR/template.ready"

for p in "$BASE_DIR" "$SANDBOX_DIR" "$ARCHIVE" "$BASE_DISK" "$DISK" "$SEED_DIR" "$SEED_ISO" "$EFI_STORE"; do
  case "$p" in
    "$ROOT"/*) ;;
    *) echo "Refusing to write outside project root: $p" >&2; exit 1 ;;
  esac
done

[[ -x "$AGENT" ]] || { echo "Missing $AGENT. Run ./scripts/bootstrap.sh first." >&2; exit 1; }

if [[ ! -f "$BASE_DISK" ]]; then
  if [[ ! -f "$ARCHIVE" ]]; then
    echo "Downloading official Debian 13 generic ARM64 image $DEBIAN_IMAGE_VERSION..."
    echo "  destination: $ARCHIVE"
    curl -fL --retry 3 --progress-bar "$BASE_URL/$ARCHIVE_NAME" -o "$ARCHIVE"
  else
    echo "Using cached archive: $ARCHIVE"
  fi

  echo "Downloading Debian SHA512SUMS..."
  curl -fsSL "$BASE_URL/SHA512SUMS" -o "$BASE_DIR/SHA512SUMS"
  expected="$(grep "  $ARCHIVE_NAME$" "$BASE_DIR/SHA512SUMS" | awk '{print $1}')"
  [[ -n "$expected" ]] || { echo "Unable to find $ARCHIVE_NAME in SHA512SUMS" >&2; exit 1; }
  actual="$(shasum -a 512 "$ARCHIVE" | awk '{print $1}')"
  [[ "$actual" == "$expected" ]] || { echo "Checksum mismatch for $ARCHIVE_NAME" >&2; exit 1; }

  echo "Extracting raw Debian base disk..."
  tmp="$BASE_DIR/extract"
  rm -rf "$tmp"; mkdir -p "$tmp"
  tar -xJf "$ARCHIVE" -C "$tmp"
  extracted="$(find "$tmp" -maxdepth 2 -type f -name 'disk.raw' -print -quit)"
  [[ -n "$extracted" ]] || { echo "disk.raw not found in archive" >&2; exit 1; }
  mv "$extracted" "$BASE_DISK"
  rm -rf "$tmp"
fi

if [[ ! -f "$DISK" ]]; then
  echo "Creating writable guest disk inside project..."
  if ! cp -c "$BASE_DISK" "$DISK" 2>/dev/null; then
    cp "$BASE_DISK" "$DISK"
  fi
else
  echo "Keeping existing guest disk: $DISK"
fi

# The Debian cloud image is intentionally small. Docker plus three AI harnesses
# need more room, so grow the raw disk sparsely to 12 GiB. Writing one byte at
# the final offset extends the file without eagerly allocating the gap on APFS.
TARGET_DISK_BYTES=$((12 * 1024 * 1024 * 1024))
CURRENT_DISK_BYTES="$(stat -f %z "$DISK")"
if (( CURRENT_DISK_BYTES < TARGET_DISK_BYTES )); then
  echo "Expanding writable guest disk sparsely to 12 GiB..."
  dd if=/dev/zero of="$DISK" bs=1 count=1 seek=$((TARGET_DISK_BYTES - 1)) conv=notrunc 2>/dev/null
fi

cat > "$SEED_DIR/meta-data" <<'META'
instance-id: magical-sandboxes-template
local-hostname: msbx
META

AGENT_B64="$(base64 < "$AGENT" | tr -d '\n')"
cat > "$SEED_DIR/user-data" <<USERDATA
#cloud-config
growpart:
  mode: auto
  devices: ['/']
resize_rootfs: true
disable_root: false
ssh_pwauth: false
write_files:
  - path: /etc/default/grub.d/99-msbx-console.cfg
    permissions: '0644'
    content: |
      GRUB_CMDLINE_LINUX_DEFAULT="console=hvc0"
      GRUB_CMDLINE_LINUX="console=hvc0"
      GRUB_TERMINAL=console
  - path: /etc/msbx-versions
    permissions: '0644'
    content: |
      DEBIAN_APT_SNAPSHOT=${DEBIAN_APT_SNAPSHOT}
      DOCKER_CE_PACKAGE_VERSION=${DOCKER_CE_PACKAGE_VERSION}
      DOCKER_CE_CLI_PACKAGE_VERSION=${DOCKER_CE_CLI_PACKAGE_VERSION}
      CONTAINERD_PACKAGE_VERSION=${CONTAINERD_PACKAGE_VERSION}
      DOCKER_BUILDX_PACKAGE_VERSION=${DOCKER_BUILDX_PACKAGE_VERSION}
      DOCKER_COMPOSE_PACKAGE_VERSION=${DOCKER_COMPOSE_PACKAGE_VERSION}
  - path: /etc/systemd/system/serial-getty@hvc0.service.d/autologin.conf
    permissions: '0644'
    content: |
      [Service]
      ExecStart=
      ExecStart=-/sbin/agetty --autologin msbx --noclear %I 115200,38400,9600 \$TERM
  - path: /usr/local/libexec/msbx-agent
    permissions: '0755'
    encoding: b64
    content: $AGENT_B64
  - path: /etc/systemd/network/20-msbx-virtio.network
    permissions: '0644'
    content: |
      [Match]
      Driver=virtio_net

      [Network]
      DHCP=yes
      IPv6AcceptRA=yes
      DNSDefaultRoute=yes
  - path: /etc/tmpfiles.d/msbx.conf
    permissions: '0644'
    content: |
      d /run/msbx 0700 msbx msbx -
  - path: /usr/local/sbin/msbx-create-user
    permissions: '0755'
    content: |
      #!/bin/sh
      set -eux
      if ! id msbx >/dev/null 2>&1; then
        useradd --create-home --uid ${HOST_UID} --user-group --shell /bin/bash msbx
      fi
      install -d -o msbx -g msbx -m 0700 /run/msbx
  - path: /usr/local/sbin/msbx-clean-codex-runtime
    permissions: '0755'
    content: |
      #!/bin/sh
      set -eu
      socket=/home/msbx/.codex/app-server-control/app-server-control.sock
      if [ -L "\$socket" ] && [ ! -e "\$socket" ]; then
        rm -f -- "\$socket"
      fi
  - path: /etc/systemd/system/msbx-agent.service
    permissions: '0644'
    content: |
      [Unit]
      Description=magical-sandboxes guest agent
      After=local-fs.target

      [Service]
      Type=simple
      ExecStartPre=/usr/local/sbin/msbx-clean-codex-runtime
      ExecStart=/usr/local/libexec/msbx-agent --port 4050
      Restart=always
      RestartSec=1

      [Install]
      WantedBy=multi-user.target
  - path: /usr/local/sbin/msbx-install-docker
    permissions: '0755'
    content: |
      #!/bin/sh
      set -eux
      . /etc/msbx-versions
      export DEBIAN_FRONTEND=noninteractive
      cat >/etc/apt/sources.list.d/debian.sources <<EOF
      Types: deb
      URIs: https://snapshot.debian.org/archive/debian/\$DEBIAN_APT_SNAPSHOT
      Suites: trixie trixie-updates
      Components: main
      Check-Valid-Until: no

      Types: deb
      URIs: https://snapshot.debian.org/archive/debian-security/\$DEBIAN_APT_SNAPSHOT
      Suites: trixie-security
      Components: main
      Check-Valid-Until: no
      EOF
      apt-get update
      apt-get install -y ca-certificates curl bubblewrap
      install -m 0755 -d /etc/apt/keyrings
      curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
      chmod a+r /etc/apt/keyrings/docker.asc
      . /etc/os-release
      ARCH="\$(dpkg --print-architecture)"
      cat >/etc/apt/sources.list.d/docker.sources <<EOF
      Types: deb
      URIs: https://download.docker.com/linux/debian
      Suites: \${VERSION_CODENAME}
      Components: stable
      Architectures: \${ARCH}
      Signed-By: /etc/apt/keyrings/docker.asc
      EOF
      apt-get update
      apt-get install -y \\
        "docker-ce=\$DOCKER_CE_PACKAGE_VERSION" \\
        "docker-ce-cli=\$DOCKER_CE_CLI_PACKAGE_VERSION" \\
        "containerd.io=\$CONTAINERD_PACKAGE_VERSION" \\
        "docker-buildx-plugin=\$DOCKER_BUILDX_PACKAGE_VERSION" \\
        "docker-compose-plugin=\$DOCKER_COMPOSE_PACKAGE_VERSION"
      systemctl enable docker.service containerd.service
      systemctl start containerd.service
      systemctl start docker.service
      docker version
      docker compose version
  - path: /usr/local/sbin/msbx-provision-command
    permissions: '0755'
    content: |
      #!/bin/bash
      set -u -o pipefail
      NAME="\$1"
      LIMIT="\$2"
      shift 2
      LOG=/var/log/msbx-provision.log
      console() { printf '%s\n' "\$*" | tee -a "\$LOG" >/dev/hvc0; }
      console "[msbx-provision] START \$NAME"
      set +e
      timeout "\$LIMIT" "\$@" 2>&1 | tee -a "\$LOG" /dev/hvc0
      rc=\${PIPESTATUS[0]}
      set -e
      if [ "\$rc" -eq 0 ]; then
        console "[msbx-provision] DONE  \$NAME"
        exit 0
      fi
      if [ "\$rc" -eq 124 ]; then
        console "[msbx-provision] ERROR \$NAME timed out after \$LIMIT"
      else
        console "[msbx-provision] ERROR \$NAME exited with status \$rc"
      fi
      console "[msbx-provision] Last log lines:"
      tail -n 80 "\$LOG" | tee /dev/hvc0 >/dev/null || true
      sync
      exit "\$rc"
  - path: /usr/local/sbin/msbx-provision-all
    permissions: '0755'
    content: |
      #!/bin/bash
      set -euo pipefail
      abort_provision() {
        rc=\$?
        printf '%s\n' "[msbx-provision] ABORT status=\$rc" >/dev/hvc0
        sync
        systemctl poweroff --no-block || true
        exit "\$rc"
      }
      trap abort_provision ERR
      /usr/local/sbin/msbx-provision-command Docker 10m /usr/local/sbin/msbx-install-docker
      usermod -aG docker msbx
      systemd-tmpfiles --create /etc/tmpfiles.d/msbx.conf
      /usr/local/sbin/msbx-provision-command Codex 10m /usr/local/sbin/msbx-install-codex
      /usr/local/sbin/msbx-provision-command Claude 10m /usr/local/sbin/msbx-install-claude
      /usr/local/sbin/msbx-provision-command OpenCode 10m /usr/local/sbin/msbx-install-opencode
      touch /etc/cloud/cloud-init.disabled
      : > /etc/machine-id
      rm -f /var/lib/dbus/machine-id
      ln -s /etc/machine-id /var/lib/dbus/machine-id
      printf '%s\n' '[msbx-provision] TEMPLATE READY' >/dev/hvc0
  - path: /usr/local/sbin/msbx-install-codex
    permissions: '0755'
    content: |
      #!/bin/sh
      set -eux
      export HOME=/root
      export PATH=/root/.local/bin:/root/bin:/usr/local/bin:/usr/bin:/bin
      curl -fsSL https://chatgpt.com/codex/install.sh -o /tmp/codex-install.sh
      sh /tmp/codex-install.sh
      CODEX_BIN="\$(command -v codex || true)"
      if [ -z "\$CODEX_BIN" ]; then
        for candidate in /root/.local/bin/codex /root/bin/codex; do
          if [ -x "\$candidate" ]; then CODEX_BIN="\$candidate"; break; fi
        done
      fi
      test -n "\$CODEX_BIN"
      CODEX_REAL="\$(readlink -f "\$CODEX_BIN")"
      test -x "\$CODEX_REAL"
      CODEX_DIR="\$(dirname "\$CODEX_REAL")"
      CODEX_PACKAGE_ROOT="\$(dirname "\$CODEX_DIR")"
      CODE_MODE_HOST="\$CODEX_DIR/codex-code-mode-host"
      test -x "\$CODE_MODE_HOST"
      test -f "\$CODEX_PACKAGE_ROOT/codex-package.json"

      # Keep the entire upstream standalone package intact. The shared
      # background server discovers package metadata/resources relative to
      # the Codex executable, so copying only bin/codex is insufficient.
      rm -rf /opt/msbx/codex/current
      install -d -m 0755 /opt/msbx/codex
      cp -a "\$CODEX_PACKAGE_ROOT" /opt/msbx/codex/current
      chmod -R a+rX /opt/msbx/codex/current

      rm -f /usr/local/bin/codex /usr/local/bin/codex-code-mode-host
      ln -s /opt/msbx/codex/current/bin/codex /usr/local/bin/codex
      ln -s /opt/msbx/codex/current/bin/codex-code-mode-host /usr/local/bin/codex-code-mode-host
      test -x /usr/local/bin/codex
      test -x /usr/local/bin/codex-code-mode-host
      /usr/local/bin/codex --version
  - path: /usr/local/sbin/msbx-install-claude
    permissions: '0755'
    content: |
      #!/bin/sh
      set -eux
      CLAUDE_HOME=/home/msbx
      CLAUDE_BIN=\$CLAUDE_HOME/.local/bin/claude
      curl -fsSL https://claude.ai/install.sh -o /tmp/claude-install.sh
      chmod 0644 /tmp/claude-install.sh
      runuser -u msbx -- env HOME=\$CLAUDE_HOME PATH=\$CLAUDE_HOME/.local/bin:/usr/local/bin:/usr/bin:/bin bash /tmp/claude-install.sh
      test -L "\$CLAUDE_BIN"
      CLAUDE_REAL="\$(readlink -f "\$CLAUDE_BIN")"
      case "\$CLAUDE_REAL" in
        \$CLAUDE_HOME/.local/share/claude/versions/*) ;;
        *) echo "Claude native installer created an unexpected launcher: \$CLAUDE_BIN -> \$CLAUDE_REAL" >&2; exit 1 ;;
      esac
      test -x "\$CLAUDE_REAL"
      rm -f /usr/local/bin/claude
      runuser -u msbx -- env HOME=\$CLAUDE_HOME PATH=\$CLAUDE_HOME/.local/bin:/usr/local/bin:/usr/bin:/bin "\$CLAUDE_BIN" --version
  - path: /usr/local/sbin/msbx-install-opencode
    permissions: '0755'
    content: |
      #!/bin/sh
      set -eux
      OPENCODE_INSTALLER=/tmp/opencode-install.sh
      curl -fsSL https://opencode.ai/install -o "\$OPENCODE_INSTALLER"
      chmod 0644 "\$OPENCODE_INSTALLER"
      runuser -u msbx -- env HOME=/home/msbx PATH=/home/msbx/.opencode/bin:/usr/local/bin:/usr/bin:/bin bash "\$OPENCODE_INSTALLER" --no-modify-path
      OPENCODE_BIN=/home/msbx/.opencode/bin/opencode
      test -x "\$OPENCODE_BIN"
      ln -sfn "\$OPENCODE_BIN" /usr/local/bin/opencode
      runuser -u msbx -- env HOME=/home/msbx PATH=/home/msbx/.opencode/bin:/usr/local/bin:/usr/bin:/bin "\$OPENCODE_BIN" --version
      rm -f "\$OPENCODE_INSTALLER"
  - path: /usr/local/libexec/msbx-run-opencode
    permissions: '0755'
    content: |
      #!/bin/bash
      set -euo pipefail
      export HOME=/home/msbx
      export PATH=/usr/local/bin:/usr/bin:/bin
      unset XDG_DATA_HOME XDG_CONFIG_HOME XDG_CACHE_HOME XDG_STATE_HOME

      OPENCODE_BIN=/usr/local/bin/opencode
      exec "\$OPENCODE_BIN" "\$@"
  - path: /usr/local/libexec/msbx-run-claude
    permissions: '0755'
    content: |
      #!/bin/bash
      set -euo pipefail

      CLAUDE_BIN=/home/msbx/.local/bin/claude
      export HOME=/home/msbx
      export PATH=/usr/local/bin:/usr/bin:/bin
      export PATH="\$HOME/.local/bin:\$PATH"
      if [ ! -L "\$CLAUDE_BIN" ]; then
        echo "msbx-run-claude: native Claude launcher is missing or is not a symlink: \$CLAUDE_BIN" >&2
        exit 1
      fi
      CLAUDE_REAL="\$(readlink -f "\$CLAUDE_BIN" 2>/dev/null || true)"
      case "\$CLAUDE_REAL" in
        /home/msbx/.local/share/claude/versions/*) ;;
        *) echo "msbx-run-claude: Claude launcher does not point into the native versions directory: \$CLAUDE_BIN -> \$CLAUDE_REAL" >&2; exit 1 ;;
      esac

      CLAUDE_HOME=/home/msbx/.claude
      mkdir -p "\$CLAUDE_HOME"
      chmod 700 "\$CLAUDE_HOME"
      export CLAUDE_CONFIG_DIR="\$CLAUDE_HOME"
      exec "\$CLAUDE_BIN" "\$@"
  - path: /usr/local/libexec/msbx-run-codex
    permissions: '0755'
    content: |
      #!/bin/bash
      set -euo pipefail

      CODEX_BIN=/usr/local/bin/codex
      CODEX_HOME_DIR=/home/msbx/.codex
      mkdir -p "\$CODEX_HOME_DIR"
      chmod 700 "\$CODEX_HOME_DIR"
      export CODEX_HOME="\$CODEX_HOME_DIR"
      exec "\$CODEX_BIN" "\$@"
runcmd:
  - [ update-grub ]
  - [ systemctl, daemon-reload ]
  - [ systemctl, enable, systemd-networkd.service ]
  - [ systemctl, disable, systemd-networkd-wait-online.service ]
  - [ systemctl, enable, systemd-resolved.service ]
  - [ systemctl, restart, systemd-networkd.service ]
  - [ systemctl, restart, systemd-resolved.service ]
  - [ /usr/local/sbin/msbx-create-user ]
  - [ systemctl, enable, serial-getty@hvc0.service ]
  - [ systemctl, enable, msbx-agent.service ]
  - [ /usr/local/sbin/msbx-provision-all ]
power_state:
  mode: poweroff
  timeout: 30
  condition: true
USERDATA

rm -f "$SEED_ISO" "$SEED_ISO.iso"
echo "Creating local cloud-init seed image:"
echo "  $SEED_ISO"
hdiutil makehybrid -quiet -iso -joliet -default-volume-name cidata -o "$SEED_ISO" "$SEED_DIR"
if [[ ! -f "$SEED_ISO" && -f "$SEED_ISO.iso" ]]; then
  mv "$SEED_ISO.iso" "$SEED_ISO"
fi
[[ -f "$SEED_ISO" ]] || { echo "Failed to create seed ISO" >&2; exit 1; }

cat <<INFO

Prepared the guest entirely inside the repository:
  base disk:     $BASE_DISK
  writable disk: $DISK
  guest agent:   $AGENT
  cloud seed:    $SEED_ISO
  EFI store:     $EFI_STORE

No magical-sandboxes file was written outside:
  $ROOT

Next:
  ./scripts/provision.sh
INFO
