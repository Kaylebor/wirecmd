#!/usr/bin/env bash
# Run one fresh comparative-evaluation arm through Codex's permission profile.
#
# The profile applies to every model tool path, including Code Mode. Codex keeps
# its normal host-side login; no credential is copied into, or readable from,
# the evaluated arm.

set -euo pipefail
IFS=$'\n\t'
umask 077

usage() {
    cat <<'EOF'
Usage:
  codex-profile-exec.sh --codex-bin PATH --arm-root DIR --model ID [OPTIONS]

The fresh arm root must contain these real directories:

  workspace/  evaluated working directory (writable)
  tools/      condition-specific executables and fixtures (read-only)
  inputs/     condition-specific configuration and task inputs (read-only)
  runtime/    condition-specific runtime directory (read-only in this slice)
  state/      condition-specific client state (writable)

By default --prompt is ARM_ROOT/inputs/prompt.txt. JSONL events are written to
ARM_ROOT/transcript.jsonl; an existing transcript is rejected.

--codex-bin is the exact native Codex executable installed for the evaluator.
It is allowed read access only so Codex's own sandbox can start its runner.

Capability option:
  --allow-network  Enable direct command networking. On Linux this is required
                   for both the assigned Unix daemon socket and loopback HTTP;
                   Codex's exact Unix-socket proxy rules are macOS-only.
  --capability-config PATH  Set CAPABILITY_CONFIG to an assigned input file.
  --native-mcp-root DIR     Parent-only directory holding native stdio MCP
                            wrappers. Required when --mcp-stdio is supplied;
                            it is never filesystem-granted to model commands.
  --mcp-stdio NAME=PATH     Register one pinned stdio MCP wrapper beneath
                            --native-mcp-root.
  --mcp-http NAME=URL       Register one loopback Streamable HTTP MCP server.
                            Repeat either option as needed for the native arm.
EOF
}

fail() {
    printf '%s\n' "codex-profile-exec: $*" >&2
    exit 2
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

# Emit one TOML basic string. All generated profile values are local paths, but
# quoting them still avoids treating a legitimate space or quote as TOML syntax.
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
model=
prompt=
allow_network=false
capability_config=
native_mcp_root=
mcp_stdio=()
mcp_http=()

while (($#)); do
    case $1 in
        --codex-bin) codex_bin=${2-}; shift 2 ;;
        --arm-root) arm_root=${2-}; shift 2 ;;
        --model) model=${2-}; shift 2 ;;
        --prompt) prompt=${2-}; shift 2 ;;
        --allow-network) allow_network=true; shift ;;
        --capability-config) capability_config=${2-}; shift 2 ;;
        --native-mcp-root) native_mcp_root=${2-}; shift 2 ;;
        --mcp-stdio) mcp_stdio+=("${2-}"); shift 2 ;;
        --mcp-http) mcp_http+=("${2-}"); shift 2 ;;
        --help|-h) usage; exit 0 ;;
        *) fail "unknown argument: $1" ;;
    esac
done

[[ -n $codex_bin && -n $arm_root && -n $model ]] || {
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

if ((${#mcp_stdio[@]})); then
    [[ -n $native_mcp_root ]] || fail "--native-mcp-root is required with --mcp-stdio"
    [[ ! -L $arm_root/parent ]] || fail "parent-only native MCP directory must not be a symlink"
    parent_dir=$(absolute_dir "$arm_root/parent")
    native_mcp_root=$(absolute_dir "$native_mcp_root")
    [[ $native_mcp_root == "$parent_dir"/* ]] || fail "--native-mcp-root must be beneath ARM_ROOT/parent"
elif [[ -n $native_mcp_root ]]; then
    fail "--native-mcp-root is only valid with --mcp-stdio"
fi

if [[ -z $prompt ]]; then
    prompt=$inputs_dir/prompt.txt
fi
prompt=$(absolute_file "$prompt")
[[ $prompt == "$arm_root"/* ]] || fail "--prompt must be inside --arm-root"
transcript=$arm_root/transcript.jsonl
stderr_log=$arm_root/stderr.log
metadata=$arm_root/run.meta
for output in "$transcript" "$stderr_log" "$metadata"; do
    [[ ! -e $output && ! -L $output ]] || fail "refusing to overwrite existing run artifact: $output"
done

codex_runtime_dir=$(absolute_dir "$(dirname -- "$codex_bin")")
state_toml=$(toml_string "$state_dir")
tools_toml=$(toml_string "$tools_dir")
inputs_toml=$(toml_string "$inputs_dir")
runtime_toml=$(toml_string "$runtime_dir")
codex_runtime_toml=$(toml_string "$codex_runtime_dir")
path_toml=$(toml_string "$tools_dir:/usr/bin:/bin")
home_toml=$(toml_string "$state_dir/home")
tmp_toml=$(toml_string "$state_dir/tmp")
runtime_env_toml=$(toml_string "$runtime_dir")
if [[ -n $capability_config ]]; then
    capability_config=$(absolute_file "$capability_config")
    [[ $capability_config == "$inputs_dir"/* ]] || fail "capability config must be inside the assigned inputs directory"
else
    capability_config=$inputs_dir/config
fi
capability_config_toml=$(toml_string "$capability_config")

# This deliberately names individual arm directories rather than its parent:
# files placed beside them, other evaluation arms, the repository, and host
# Codex state do not become visible merely because the arm root is trusted.
filesystem="{\":minimal\"=\"read\",$codex_runtime_toml=\"read\",$tools_toml=\"read\",$inputs_toml=\"read\",$runtime_toml=\"read\",$state_toml=\"write\",\":workspace_roots\"={\".\"=\"write\"}}"
environment="{PATH=$path_toml,HOME=$home_toml,TMPDIR=$tmp_toml,XDG_RUNTIME_DIR=$runtime_env_toml,MCPC_HOME_DIR=$state_toml,CAPABILITY_CONFIG=$capability_config_toml}"

mkdir -p "$state_dir/home" "$state_dir/tmp"

codex_args=(
    --json \
    --ephemeral \
    --ignore-user-config \
    --ignore-rules \
    --strict-config \
    --skip-git-repo-check \
    --model "$model" \
    -C "$workspace" \
    -c 'default_permissions="evaluation"' \
    -c "permissions.evaluation.filesystem=$filesystem" \
    -c 'shell_environment_policy.inherit="none"' \
    -c "shell_environment_policy.set=$environment" \
)

if [[ $allow_network == true ]]; then
    codex_args+=(
        -c 'permissions.evaluation.network={enabled=true}'
    )
fi

registered_mcp_names=" "
for spec in "${mcp_stdio[@]}"; do
    name=${spec%%=*}
    command_path=${spec#*=}
    [[ $spec == *=* && $name =~ ^[A-Za-z0-9_-]+$ ]] || fail "invalid --mcp-stdio NAME=PATH: $spec"
    [[ $registered_mcp_names != *" $name "* ]] || fail "duplicate MCP server name: $name"
    command_path=$(absolute_file "$command_path")
    [[ -x $command_path && $command_path == "$native_mcp_root"/* ]] || fail "stdio MCP command must be executable beneath --native-mcp-root: $command_path"
    command_toml=$(toml_string "$command_path")
    codex_args+=(
        -c "mcp_servers.$name={command=$command_toml,required=true,default_tools_approval_mode=\"approve\"}"
    )
    registered_mcp_names+=" $name "
done

for spec in "${mcp_http[@]}"; do
    name=${spec%%=*}
    server_url=${spec#*=}
    [[ $spec == *=* && $name =~ ^[A-Za-z0-9_-]+$ ]] || fail "invalid --mcp-http NAME=URL: $spec"
    [[ $registered_mcp_names != *" $name "* ]] || fail "duplicate MCP server name: $name"
    [[ $server_url =~ ^http://127\.0\.0\.1:[0-9]+/ ]] || fail "HTTP MCP URL must use literal 127.0.0.1: $server_url"
    url_toml=$(toml_string "$server_url")
    codex_args+=(
        -c "mcp_servers.$name={url=$url_toml,required=true,default_tools_approval_mode=\"approve\"}"
    )
done

start_ns=$(date +%s%N)
{
    printf 'format=wirecmd-eval-run-v1\n'
    printf 'start_unix_ns=%s\n' "$start_ns"
    printf 'cwd=%s\n' "$workspace"
    printf 'arm_root=%s\n' "$arm_root"
    printf 'codex_bin=%s\n' "$codex_bin"
    printf 'model=%s\n' "$model"
    printf 'prompt=%s\n' "$prompt"
    printf 'capability_config=%s\n' "$capability_config"
    printf 'native_mcp_root=%s\n' "${native_mcp_root:-none}"
    printf 'direct_command_network=%s\n' "$allow_network"
    printf 'mcp_stdio_count=%s\n' "${#mcp_stdio[@]}"
    printf 'mcp_http_count=%s\n' "${#mcp_http[@]}"
} >"$metadata"

set +e
"$codex_bin" exec "${codex_args[@]}" "$(<"$prompt")" </dev/null >"$transcript" 2>"$stderr_log"
status=$?
set -e
end_ns=$(date +%s%N)
{
    printf 'end_unix_ns=%s\n' "$end_ns"
    printf 'elapsed_ns=%s\n' "$((end_ns - start_ns))"
    printf 'exit_status=%s\n' "$status"
} >>"$metadata"
exit "$status"
