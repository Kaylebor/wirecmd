#!/usr/bin/env bash
# Offline capability gate for scripts/eval/codex-profile-exec.sh.
# It runs only `codex sandbox`, never starts a model, fixture, or network call.

set -euo pipefail
IFS=$'\n\t'
umask 077

usage() {
    cat <<'EOF'
Usage:
  profile-preflight.sh --codex-bin PATH --arm-root DIR [--auth-path PATH]
                       [--blocked-path PATH]...

The arm layout is the same as codex-profile-exec.sh. Repeat --blocked-path for
the repository, a sibling-arm sentinel, and parent-only fixture state. With no
explicit paths it defaults to this repository's go.mod. Contents are never read
or emitted.
EOF
}

fail() {
    printf '%s\n' "profile-preflight: $*" >&2
    exit 1
}

absolute_dir() {
    local path=$1 resolved
    resolved=$(realpath -e -- "$path") || fail "cannot resolve directory: $path"
    [[ -d $resolved ]] || fail "not a directory: $path"
    printf '%s\n' "$resolved"
}

absolute_file() {
    local path=$1 resolved
    resolved=$(realpath -e -- "$path") || fail "cannot resolve file: $path"
    [[ -f $resolved ]] || fail "not a regular file: $path"
    printf '%s\n' "$resolved"
}

toml_string() {
    local value=$1
    value=${value//\\/\\\\}
    value=${value//\"/\\\"}
    value=${value//$'\n'/\\n}
    value=${value//$'\r'/\\r}
    value=${value//$'\t'/\\t}
    printf '"%s"' "$value"
}

codex_bin=
arm_root=
blocked_paths=()
auth_path=${CODEX_HOME:-$HOME/.codex}/auth.json

while (($#)); do
    case $1 in
        --codex-bin) codex_bin=${2-}; shift 2 ;;
        --arm-root) arm_root=${2-}; shift 2 ;;
        --blocked-path) blocked_paths+=("${2-}"); shift 2 ;;
        --auth-path) auth_path=${2-}; shift 2 ;;
        --help|-h) usage; exit 0 ;;
        *) fail "unknown argument: $1" ;;
    esac
done

[[ -n $codex_bin && -n $arm_root ]] || {
    usage >&2
    exit 2
}
codex_bin=$(absolute_file "$codex_bin")
[[ -x $codex_bin ]] || fail "Codex executable is not executable: $codex_bin"
arm_root=$(absolute_dir "$arm_root")
[[ $arm_root != / ]] || fail "--arm-root must not be the filesystem root"
for child in workspace tools inputs runtime state; do
    [[ ! -L $arm_root/$child ]] || fail "arm subdirectory must not be a symlink: $child"
done
workspace=$(absolute_dir "$arm_root/workspace")
tools_dir=$(absolute_dir "$arm_root/tools")
inputs_dir=$(absolute_dir "$arm_root/inputs")
runtime_dir=$(absolute_dir "$arm_root/runtime")
state_dir=$(absolute_dir "$arm_root/state")
for child_path in "$workspace" "$tools_dir" "$inputs_dir" "$runtime_dir" "$state_dir"; do
    [[ $child_path == "$arm_root"/* ]] || fail "arm subdirectory escapes --arm-root: $child_path"
done

if ((${#blocked_paths[@]} == 0)); then
    script_dir=$(unset CDPATH; cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
    blocked_paths+=("$script_dir/../../go.mod")
fi
for index in "${!blocked_paths[@]}"; do
    blocked_paths[index]=$(absolute_file "${blocked_paths[index]}")
done
auth_path=$(absolute_file "$auth_path")

codex_runtime_dir=$(absolute_dir "$(dirname -- "$codex_bin")")
state_toml=$(toml_string "$state_dir")
tools_toml=$(toml_string "$tools_dir")
inputs_toml=$(toml_string "$inputs_dir")
runtime_toml=$(toml_string "$runtime_dir")
codex_runtime_toml=$(toml_string "$codex_runtime_dir")
filesystem="{\":minimal\"=\"read\",$codex_runtime_toml=\"read\",$tools_toml=\"read\",$inputs_toml=\"read\",$runtime_toml=\"read\",$state_toml=\"write\",\":workspace_roots\"={\".\"=\"write\"}}"

# Pass paths as positional arguments so this check never interpolates them into
# model-visible shell source. It reports no file content and does not need auth.
# shellcheck disable=SC2016 # The assertions run in Codex's sandboxed shell.
"$codex_bin" sandbox \
    -C "$workspace" \
    -c 'default_permissions="evaluation"' \
    -c "permissions.evaluation.filesystem=$filesystem" \
    -P evaluation -- \
    bash -ceu '
        workspace=$1
        inputs=$2
        runtime=$3
        state=$4
        auth=$5
        shift 5

        test -d "$workspace"
        test -r "$inputs"
        test -r "$runtime"
        for blocked; do
            test ! -r "$blocked"
        done
        if test -e "$auth"; then
            test ! -r "$auth"
        fi
        printf profile-preflight >"$workspace/.profile-preflight"
        printf profile-preflight >"$state/.profile-preflight"
    ' _ "$workspace" "$inputs_dir" "$runtime_dir" "$state_dir" "$auth_path" "${blocked_paths[@]}"

[[ $(<"$workspace/.profile-preflight") == profile-preflight ]] || fail "workspace was not writable"
[[ $(<"$state_dir/.profile-preflight") == profile-preflight ]] || fail "state was not writable"
rm -f -- "$workspace/.profile-preflight" "$state_dir/.profile-preflight"
printf '%s\n' 'permission-profile preflight passed: arm access works; repository and Codex auth are unreadable'
