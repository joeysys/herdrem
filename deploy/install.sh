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

Install herdrem for the current user, then enable its systemd user service.
Herdr must already be installed for this user.

By default, the installer builds when it is located in a complete source tree.
Otherwise it installs the herdrem binary next to the deploy directory.

Environment:
  HERDR_BIN             Herdr command or absolute path (default: herdr)
  HERDREM_INSTALL_DIR   herdrem install directory (default: ~/.local/bin)

Options:
  --source     require a source tree and build herdrem before install
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

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
root_dir="$(cd -- "$script_dir/.." && pwd -P)"
service_source="$script_dir/herdrem.service"
env_source="$script_dir/herdrem.env.example"
[[ -f "$service_source" ]] || die "missing deployment template $service_source"
[[ -f "$env_source" ]] || die "missing deployment template $env_source"

has_source_tree() {
    [[ -f "$root_dir/Makefile" &&
        -f "$root_dir/go.mod" &&
        -d "$root_dir/cmd/herdrem" &&
        -f "$root_dir/web/package.json" ]]
}

binary_source="$root_dir/herdrem"
if [[ "$install_mode" == auto ]]; then
    if has_source_tree; then
        install_mode=source
    elif [[ -f "$binary_source" ]]; then
        install_mode=binary
    else
        die "no source tree found and no herdrem binary exists at $binary_source"
    fi
elif [[ "$install_mode" == source ]]; then
    has_source_tree || die "--source requires a complete source tree at $root_dir"
else
    [[ -f "$binary_source" ]] || die "--binary requires $binary_source"
fi

if [[ "$install_mode" == source ]]; then
    command -v make >/dev/null 2>&1 || die 'make is required when building from source'
    binary_source="$root_dir/herdrem"
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
    else
        printf 'Input mode:    existing binary\n'
        printf 'Would use:     %s\n' "$binary_source"
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
    make -C "$root_dir" build
    [[ -f "$binary_source" ]] || die "build completed without producing $binary_source"
else
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
