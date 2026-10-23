#!/usr/bin/env bash
set -Eeuo pipefail

die() {
    printf 'error: %s\n' "$*" >&2
    exit 1
}

log() {
    printf '▸ %s\n' "$*"
}

usage() {
    cat <<'EOF'
Usage: deploy/install.sh [--source | --binary] [--dry-run] [--no-linger]
       curl -fsSL https://github.com/joeysys/herdrem/releases/latest/download/install.sh | bash

Install herdrem for the current user, then enable its systemd user service.
Herdr must already be installed for this user.

By default, the installer looks for a cloned herdrem tree — next to deploy/,
in the script's own directory, or in the current directory (which is the only
anchor available when the script is piped through bash). A usable clone is
built with `make build`; if there is no clone or the build toolchain is
incomplete or fails, the installer downloads the latest release binary from
GitHub and verifies it against checksums.txt. Without a clone, a herdrem
binary placed next to deploy/ is installed as is instead of downloading; a
clone's own herdrem binary is never a fallback for the build, and a piped run
never uses a loose herdrem file (use --binary to name one explicitly).

Environment:
  HERDR_BIN             Herdr command or absolute path (default: herdr)
  HERDREM_INSTALL_DIR   herdrem install directory (default: ~/.local/bin)
  HERDREM_DOWNLOAD_URL  release asset URL override
                        (default: latest GitHub release for this OS/arch)
  HERDREM_CHECKSUMS_URL checksums.txt URL override
                        (default: checksums.txt next to the asset URL)

Options:
  --source     require a clone and build from it (no download fallback)
  --binary     require and install the herdrem binary next to deploy/
  --dry-run    validate and print the plan without changing anything
  --no-linger  do not enable user lingering
EOF
}

dry_run=0
enable_linger=1
install_mode=auto
for arg in "$@"; do
    case "$arg" in
        --source)
            [[ "$install_mode" == auto ]] || { usage >&2; exit 2; }
            install_mode=source
            ;;
        --binary)
            [[ "$install_mode" == auto ]] || { usage >&2; exit 2; }
            install_mode=binary
            ;;
        --dry-run)
            (( dry_run == 0 )) || { usage >&2; exit 2; }
            dry_run=1
            ;;
        --no-linger)
            (( enable_linger == 1 )) || { usage >&2; exit 2; }
            enable_linger=0
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            usage >&2
            exit 2
            ;;
    esac
done

[[ -n "${HOME:-}" ]] || die 'HOME is not set'
(( EUID != 0 )) || die 'run this as the user who runs herdr, not as root'
command -v systemctl >/dev/null 2>&1 || die 'systemctl is required'
linger_user="$(id -un)"

if [[ -f "${BASH_SOURCE[0]:-}" ]]; then
    piped=0
    script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
else
    # Piped through bash (curl … | bash): stdin is the script, so there is no
    # script directory. The current directory is the only anchor, and it only
    # counts as a clone; loose files found there (a herdrem binary, unit or env
    # templates) are never trusted, so a piped run downloads and verifies.
    piped=1
    script_dir="$PWD"
fi

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/herdrem-install.XXXXXX")"
cleanup() { rm -rf -- "$work_dir"; }
trap cleanup EXIT

# Embedded copies of deploy/herdrem.service and deploy/herdrem.env.example.
# The on-disk files win when the script runs inside a checkout — keep these
# in sync with them. They exist so a piped run needs no sibling files.
embedded_service() {
    cat <<'EMBEDDED_SERVICE'
[Unit]
Description=Herdrem — per-user herdr remote relay and web PWA
Documentation=https://herdr.dev
After=network-online.target

[Service]
Type=simple
# This is a systemd user unit: it runs as the current user and uses that
# user's Herdr socket/session. The installer renders this ExecStart with the
# absolute installed Herdrem path. It does not start or supervise `herdr server`.
ExecStart=%h/.local/bin/herdrem serve --web --require-herdr
Restart=on-failure
RestartSec=3

# Load secrets (HERDREM_TOKEN etc.) from an env file.
# The `-` prefix is omitted so systemd fails to start if the file is missing.
EnvironmentFile=%h/.config/herdrem/herdrem.env

# Use the same HOME and data directory as the logged-in user. The installer
# records HERDR_BIN in the env file; without it, the relay resolves `herdr`
# from PATH at runtime.
Environment=HOME=%h
Environment=HERDREM_DATA_DIR=%h/.config/herdrem
# Fallback PATH; setup stores the actual Herdrem/Herdr directories in the env file.
Environment=PATH=%h/.local/bin:%h/bin:/usr/local/bin:/usr/bin:/bin

# Bind to loopback by default. Set HERDREM_BIND=0.0.0.0 in the env file
# for LAN access (requires HERDREM_TOKEN), or use --tunnel for public access.
Environment=HERDREM_BIND=127.0.0.1

# Hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX

[Install]
WantedBy=default.target
EMBEDDED_SERVICE
}
embedded_env() {
    cat <<'EMBEDDED_ENV'
# ~/.config/herdrem/herdrem.env — copy this file and fill in real values.
# Loaded by the herdrem binary itself at startup (and, under systemd, also by
# the user herdrem.service via EnvironmentFile=). Variables already set in the
# process environment take precedence over values here.
# systemd fails to start the unit if this file is missing.

# Relay listen port (default 8766)
# HERDREM_PORT=8766

# Bind address. Defaults to 127.0.0.1 (loopback only).
# Set to 0.0.0.0 for LAN access — HERDREM_TOKEN is REQUIRED when
# binding to a non-loopback address (the relay refuses to start otherwise).
HERDREM_BIND=127.0.0.1

# Shared-secret token for relay auth.
# REQUIRED for any non-loopback exposure (tunnels, LAN, public).
# Generate with: openssl rand -hex 16
# Example: HERDREM_TOKEN=4f8b9e...
# Note: The relay does not print this token in startup logs.
# The default HERDREM_BIND=127.0.0.1 allows an empty token only for a
# same-origin browser opened via localhost/127.0.0.1/::1. If you set a
# token, localhost needs it too. Uncomment and set for any real use
# (LAN, public, --tunnel, or locking down loopback).
# HERDREM_TOKEN=

# Optional Herdr executable name/path. If unset, `herdr` is resolved from
# PATH. The one-command installer writes the selected absolute path here and
# also persists its directory so the service keeps working after logout,
# linger, or reboot.
# HERDR_BIN=herdr

# Optional persistent PATH override for the user unit. Leave commented when
# the fallback PATH is enough; otherwise include the directories containing
# herdrem and herdr, for example:
# PATH=/path/to/herdrem/bin:/path/to/herdr/bin:/usr/local/bin:/usr/bin:/bin

# Comma-separated SSH remotes (optional, requires passwordless SSH keys)
# HERDR_REMOTES=user@server1,user@server2

# Pane kinds to broadcast: agent, shell, or both (comma-separated)
# HERDREM_KINDS=agent,shell

# Data directory for todo/prefs/poller state
# HERDREM_DATA_DIR=/home/you/.config/herdrem

# Extra ports that may be forwarded via Cloudflare tunnels, on top of
# the default allowlist 5173,3000,8080,4173. Singles or inclusive ranges.
# Privileged system ports (<1024) and malformed tokens are ignored.
# HERDREM_TUNNEL_PORTS=8000-8100,9000-10000,4174

# Allow ungated (direct) quick tunnels. Off by default — anyone with the
# generated URL would have full access to that localhost port.
# HERDREM_ALLOW_DIRECT_TUNNEL=1

# Cap on concurrent starting+active port tunnels (default 8).
# HERDREM_TUNNEL_MAX=8
EMBEDDED_ENV
}

service_source="$script_dir/herdrem.service"
env_source="$script_dir/herdrem.env.example"
if (( piped )) || [[ ! -f "$service_source" ]]; then
    service_source="$work_dir/herdrem.service"
    embedded_service >"$service_source"
fi
if (( piped )) || [[ ! -f "$env_source" ]]; then
    env_source="$work_dir/herdrem.env.example"
    embedded_env >"$env_source"
fi
[[ -s "$service_source" ]] || die "missing deployment template $service_source"
[[ -s "$env_source" ]] || die "missing deployment template $env_source"

has_source_tree() {
    local root="$1"
    [[ -f "$root/Makefile" &&
        -f "$root/go.mod" &&
        -d "$root/cmd/herdrem" &&
        -f "$root/web/package.json" ]]
}

build_possible() {
    command -v make >/dev/null 2>&1 &&
        command -v go >/dev/null 2>&1 &&
        command -v npm >/dev/null 2>&1
}

release_url() {
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    case "$(uname -m)" in
        x86_64 | amd64) arch=amd64 ;;
        aarch64 | arm64) arch=arm64 ;;
        *) return 1 ;;
    esac
    printf '%s\n' "${HERDREM_DOWNLOAD_URL:-https://github.com/joeysys/herdrem/releases/latest/download/herdrem-${os}-${arch}}"
}

fetch() {
    local url="$1" dest="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$url" -o "$dest"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$dest" "$url"
    else
        die 'curl or wget is required to download the herdrem release binary'
    fi
}

# verify_binary refuses an unverified download: the release must publish a
# checksums.txt (sha256sum format: "<sha>  <asset name>") next to the asset.
verify_binary() {
    local file="$1" url="$2" asset sums expected actual
    asset="${url##*/}"
    asset="${asset%%[?#]*}"
    sums="$work_dir/checksums.txt"
    if ! fetch "${HERDREM_CHECKSUMS_URL:-${url%/*}/checksums.txt}" "$sums"; then
        die "no checksums.txt next to $url; refusing to install an unverified binary"
    fi
    expected="$(awk -v a="$asset" '$2 == a {print $1; exit}' "$sums")"
    [[ -n "$expected" ]] || die "no sha256 entry for $asset in checksums.txt"
    command -v sha256sum >/dev/null 2>&1 || die 'sha256sum is required to verify the download'
    actual="$(sha256sum "$file" | awk '{print $1}')"
    [[ "$actual" == "$expected" ]] || die "checksum mismatch for $asset"
}

download_binary() {
    local dest="$1" url
    url="$(release_url)" || die "no prebuilt herdrem binary for $(uname -s)/$(uname -m); build from a clone instead"
    log "downloading $url"
    fetch "$url" "$dest" || die "download failed: $url"
    verify_binary "$dest" "$url"
    chmod 0755 "$dest"
}

# Root candidates, in order: a clone sitting next to deploy/ (in-repo or
# bundle run), the script's own directory, and the current directory. A piped
# run has no script directory, so its only candidate is the current directory
# (where `curl | bash` lands inside a clone) — never its parent.
candidates=()
if (( piped )); then
    cand_dirs=("$PWD")
else
    cand_dirs=("$script_dir/.." "$script_dir" "$PWD")
fi
for cand in "${cand_dirs[@]}"; do
    cand="$(cd -- "$cand" 2>/dev/null && pwd -P)" || continue
    candidates+=("$cand")
done

root_dir=''
for cand in "${candidates[@]}"; do
    if has_source_tree "$cand"; then root_dir="$cand"; break; fi
done
if [[ -z "$root_dir" ]] && (( ! piped )); then
    for cand in "${candidates[@]}"; do
        if [[ -f "$cand/herdrem" ]]; then root_dir="$cand"; break; fi
    done
fi
root_dir="${root_dir:-$PWD}"

requested_mode="$install_mode"
binary_source="$root_dir/herdrem"
if [[ "$install_mode" == auto ]]; then
    if has_source_tree "$root_dir"; then
        if build_possible; then
            install_mode=source
        else
            log 'clone found but the build toolchain is incomplete (need make, go, npm); will download a release binary'
            install_mode=download
        fi
    elif (( ! piped )) && [[ -f "$binary_source" ]]; then
        install_mode=binary
    else
        install_mode=download
    fi
elif [[ "$install_mode" == source ]]; then
    has_source_tree "$root_dir" || die "--source requires a complete source tree at $root_dir"
else
    [[ -f "$binary_source" ]] || die "--binary requires $binary_source"
fi

resolve_executable() {
    local name="$1"

    if [[ "$name" == */* ]]; then
        [[ -f "$name" && -x "$name" ]] || return 1
        (cd -- "$(dirname -- "$name")" && printf '%s/%s\n' "$PWD" "$(basename -- "$name")")
        return
    fi

    type -P -- "$name"
}

herdr_selector="${HERDR_BIN:-herdr}"
if ! herdr_path="$(resolve_executable "$herdr_selector")"; then
    die "herdr was not found; install it first, add it to PATH, or set HERDR_BIN=/absolute/path/to/herdr"
fi
herdr_bin_dir="$(cd -- "$(dirname -- "$herdr_path")" && pwd -P)"

install_dir="${HERDREM_INSTALL_DIR:-$HOME/.local/bin}"
[[ "$install_dir" == /* ]] || die 'HERDREM_INSTALL_DIR must be an absolute path'
herdrem_path="$install_dir/herdrem"

config_dir="$HOME/.config/herdrem"
env_file="$config_dir/herdrem.env"
unit_dir="$HOME/.config/systemd/user"
unit_file="$unit_dir/herdrem.service"

if (( dry_run )); then
    printf 'Dry run: no files or services will be changed.\n\n'
    if [[ "$install_mode" == source ]]; then
        printf 'Input mode:    build from source\n'
        printf 'Would build:   make -C %s build\n' "$root_dir"
    elif [[ "$install_mode" == binary ]]; then
        printf 'Input mode:    existing binary\n'
        printf 'Would use:     %s\n' "$binary_source"
    else
        printf 'Input mode:    download release binary\n'
        printf 'Would fetch:   %s\n' "$(release_url || printf '<no prebuilt asset for %s/%s>' "$(uname -s)" "$(uname -m)")"
    fi
    printf 'Would install: %s\n' "$herdrem_path"
    printf 'Herdr:         %s\n' "$herdr_path"
    printf 'Environment:   %s (create if missing, preserve user values)\n' "$env_file"
    printf 'User unit:     %s (write absolute ExecStart, preserve custom units)\n' "$unit_file"
    printf 'Systemd:       daemon-reload, enable, restart herdrem.service\n'
    if (( enable_linger )); then
        printf 'Linger:        enable for %s\n' "$linger_user"
    else
        printf 'Linger:        leave unchanged\n'
    fi
    exit 0
fi

if [[ "$install_mode" == source ]]; then
    log "building herdrem"
    if ! make -C "$root_dir" build || [[ ! -f "$binary_source" ]]; then
        if [[ "$requested_mode" == source ]]; then
            die "build failed in $root_dir"
        fi
        log "warning: build failed; downloading a release binary instead"
        install_mode=download
    fi
fi
if [[ "$install_mode" == download ]]; then
    binary_source="$work_dir/herdrem"
    download_binary "$binary_source"
elif [[ "$install_mode" == binary ]]; then
    log "using existing binary at $binary_source"
fi

log "installing herdrem to $install_dir"
mkdir -p "$install_dir"
if [[ -e "$herdrem_path" && "$binary_source" -ef "$herdrem_path" ]]; then
    chmod 0755 "$herdrem_path"
    log "herdrem is already installed at $herdrem_path"
else
    install -m 0755 "$binary_source" "$herdrem_path"
fi

mkdir -p "$config_dir" "$unit_dir"

if [[ ! -e "$env_file" ]]; then
    install -m 0600 "$env_source" "$env_file"
else
    chmod 0600 "$env_file"
fi

systemd_quote() {
    local value="$1"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    printf '"%s"' "$value"
}

path_begin='# BEGIN herdrem-install.sh managed PATH'
path_end='# END herdrem-install.sh managed PATH'
path_value="$install_dir:$herdr_bin_dir:/usr/local/bin:/usr/bin:/bin"
herdr_line="HERDR_BIN=$(systemd_quote "$herdr_path")"
path_line="PATH=$(systemd_quote "$path_value")"

if grep -qFx -- "$path_begin" "$env_file"; then
    grep -qFx -- "$path_end" "$env_file" || die "incomplete managed PATH block in $env_file"
    temp_env="$(mktemp "${env_file}.XXXXXX")"
    awk -v begin="$path_begin" -v end="$path_end" '
        $0 == begin { skip = 1; next }
        skip && $0 == end { skip = 0; next }
        !skip { print }
    ' "$env_file" >"$temp_env"
    printf '\n%s\n%s\n%s\n%s\n' "$path_begin" "$herdr_line" "$path_line" "$path_end" >>"$temp_env"
    chmod 0600 "$temp_env"
    mv -- "$temp_env" "$env_file"
else
    printf '\n%s\n%s\n%s\n%s\n' "$path_begin" "$herdr_line" "$path_line" "$path_end" >>"$env_file"
    chmod 0600 "$env_file"
fi

render_unit() {
    local target="$1"
    local exec_line="ExecStart=$(systemd_quote "$herdrem_path") serve --web --require-herdr"
    local sed_line

    sed_line="${exec_line//\\/\\\\}"
    sed_line="${sed_line//&/\\&}"
    sed_line="${sed_line//|/\\|}"
    sed -E "s|^ExecStart=.*$|$sed_line|" "$service_source" >"$target"
}

is_stock_unit() {
    local candidate="$1"

    sed -E \
        -e '/^[[:space:]]*#/d' \
        -e '/^[[:space:]]*$/d' \
        -e 's|^ExecStart=.* serve --web --require-herdr$|ExecStart=__HERDREM__ serve --web --require-herdr|' \
        "$candidate"
}

temp_unit="$(mktemp "${unit_dir}/.herdrem.service.XXXXXX")"
render_unit "$temp_unit"
if [[ ! -e "$unit_file" ]]; then
    install -m 0644 "$temp_unit" "$unit_file"
elif is_stock_unit "$unit_file" | cmp -s - <(is_stock_unit "$service_source"); then
    install -m 0644 "$temp_unit" "$unit_file"
else
    log "keeping existing customized unit at $unit_file"
fi
rm -f -- "$temp_unit"

log 'enabling and restarting herdrem.service'
systemctl --user daemon-reload
systemctl --user enable herdrem.service
systemctl --user restart herdrem.service

if (( enable_linger )); then
    if ! command -v loginctl >/dev/null 2>&1; then
        log "warning: loginctl not found; skipped user lingering for $linger_user"
    elif ! loginctl enable-linger "$linger_user"; then
        log "warning: failed to enable lingering for $linger_user; service is still enabled for this login"
    fi
fi

printf '\n✓ herdrem is running as a systemd user service\n'
printf '  status: systemctl --user status herdrem.service\n'
printf '  logs:   journalctl --user -u herdrem.service -f\n'
printf '  local:  http://127.0.0.1:8766\n'
