#!/usr/bin/env bash
# Parent-owned lifecycle executor for a matrix staged by
# prepare-comparative-matrix.sh. It intentionally runs every arm in plan.tsv
# in order and leaves model execution to codex-profile-exec.sh.

set -euo pipefail
IFS=$'\n\t'
umask 077

usage() {
    cat <<'EOF'
Usage:
  run-comparative-matrix.sh --run-root DIR --codex-bin PATH

Run every staged arm in DIR/plan.tsv sequentially. The run root must have been
created by prepare-comparative-matrix.sh and must not contain an existing
transcript for any arm. This command starts fixtures and evaluator models.
EOF
}

fail() {
    printf '%s\n' "run-comparative-matrix: $*" >&2
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

run_root=
codex_bin=
while (($#)); do
    case $1 in
        --run-root) run_root=${2-}; shift 2 ;;
        --codex-bin) codex_bin=${2-}; shift 2 ;;
        --help|-h) usage; exit 0 ;;
        *) fail "unknown argument: $1" ;;
    esac
done
[[ -n $run_root && -n $codex_bin ]] || {
    usage >&2
    exit 2
}
run_root=$(absolute_dir "$run_root")
codex_bin=$(absolute_file "$codex_bin")
[[ -x $codex_bin ]] || fail "Codex executable is not executable: $codex_bin"
plan=$run_root/plan.tsv
manifest=$run_root/parent/input-manifest.env
[[ -f $plan && -f $manifest ]] || fail "run root is not a staged comparative matrix"

script_dir=$(unset CDPATH; cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
profile_runner=$script_dir/codex-profile-exec.sh
profile_preflight=$script_dir/profile-preflight.sh
repo_root=$(unset CDPATH; cd -- "$script_dir/../.." && pwd -P)
[[ -x $profile_runner ]] || fail "profile runner is not executable: $profile_runner"
[[ -x $profile_preflight ]] || fail "profile preflight is not executable: $profile_preflight"
command -v curl >/dev/null || fail "curl is required for parent HTTP readiness"
command -v setsid >/dev/null || fail "setsid is required for child cleanup"
command -v jq >/dev/null || fail "jq is required for parent transcript verification"
command -v pgrep >/dev/null || fail "pgrep is required for mcpc child checks"

active_conformance_pid=
active_daemon_pid=
active_native_root=
current_parent=
# These are populated from parent/arm.env, which is generated with printf %q
# by the staging script. Initialize them for shellcheck and reset them before
# every source operation below.
sequence=
run_id=
condition=
preparation=
repetition=
model=
http_port=
http_url=
capability_config=
conformance_binary=
native_mcp_root=
native_stdio_memory=
native_stdio_everything=
native_http=
tools=
state=
runtime=

timestamp_ns() {
    date +%s%N
}

event() {
    local label=$1
    shift
    [[ -n $current_parent ]] || return 0
    printf '%s\t%s\t%s\n' "$(timestamp_ns)" "$label" "$*" >>"$current_parent/lifecycle.tsv"
}

capture() {
    local label=$1 stdout=$2 stderr=$3
    shift 3
    local start status end
    start=$(timestamp_ns)
    set +e
    "$@" >"$stdout" 2>"$stderr"
    status=$?
    set -e
    end=$(timestamp_ns)
    event "$label" "start_ns=$start end_ns=$end exit_status=$status stdout=$(basename -- "$stdout") stderr=$(basename -- "$stderr")"
    return "$status"
}

stop_session() {
    local label=$1 pid=$2
    [[ -n $pid ]] || return 0
    if ! kill -0 "$pid" 2>/dev/null; then
        wait "$pid" 2>/dev/null || true
        event "$label" "already_exited pid=$pid"
        return 0
    fi
    event "$label" "term_process_group=$pid"
    kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
    for _ in {1..50}; do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.1
    done
    if kill -0 "$pid" 2>/dev/null; then
        event "$label" "kill_process_group=$pid"
        kill -KILL -- "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
    fi
    wait "$pid" 2>/dev/null || true
}

cleanup_native_children() {
    local root=$1 pid
    [[ -n $root && -d $root ]] || return 0
    while IFS= read -r pid; do
        [[ $pid =~ ^[0-9]+$ ]] || continue
        event native_cleanup "term_pid=$pid"
        kill -TERM "$pid" 2>/dev/null || true
    done < <(pgrep -f -- "$root" 2>/dev/null || true)
    sleep 0.1
    while IFS= read -r pid; do
        [[ $pid =~ ^[0-9]+$ ]] || continue
        event native_cleanup "kill_pid=$pid"
        kill -KILL "$pid" 2>/dev/null || true
    done < <(pgrep -f -- "$root" 2>/dev/null || true)
}

cleanup_active() {
    cleanup_native_children "$active_native_root"
    stop_session daemon "$active_daemon_pid"
    stop_session conformance "$active_conformance_pid"
    active_conformance_pid=
    active_daemon_pid=
    active_native_root=
}
trap 'cleanup_active' EXIT HUP INT TERM

wait_for_conformance() {
    local url=$1 logs=$2 body headers
    body=$logs/conformance-readiness.body
    headers=$logs/conformance-readiness.headers
    local payload='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28","capabilities":{},"clientInfo":{"name":"wirecmd-eval-parent","version":"1"}}}'
    local status
    for _ in {1..100}; do
        set +e
        curl --silent --show-error --fail --max-time 1 \
            -D "$headers" -o "$body" \
            -H 'Content-Type: application/json' \
            -H 'Accept: application/json, text/event-stream' \
            --data "$payload" "$url" >>"$logs/conformance-readiness.stderr" 2>&1
        status=$?
        set -e
        if ((status == 0)) && grep -q '"result"' "$body"; then
            event conformance_readiness "url=$url status=ready"
            return 0
        fi
        sleep 0.1
    done
    event conformance_readiness "url=$url status=failed"
    return 1
}

wait_for_daemon() {
    local wirecmd=$1 runtime=$2 logs=$3
    local status
    for _ in {1..100}; do
        set +e
        XDG_RUNTIME_DIR=$runtime "$wirecmd" daemon status >"$logs/daemon-status.json" 2>"$logs/daemon-status.stderr"
        status=$?
        set -e
        if ((status == 0)); then
            event daemon_readiness 'status=ready'
            return 0
        fi
        sleep 0.1
    done
    event daemon_readiness 'status=failed'
    return 1
}

empty_graph_file() {
    local path=$1
    jq -e '(.result.data // .structuredContent) | type == "object" and .entities == null and .relations == null' "$path" >/dev/null
}

source_arm_env() {
    local path=$1
    [[ -f $path && ! -L $path ]] || fail "missing or symlinked parent arm metadata: $path"
    # arm.env is generated only by prepare-comparative-matrix.sh using printf
    # %q. The run root is parent-owned and a staged arm is single-use.
    sequence='' run_id='' condition='' preparation='' repetition='' model=''
    http_port='' http_url='' capability_config='' conformance_binary=''
    native_mcp_root='' native_stdio_memory='' native_stdio_everything='' native_http=''
    tools='' state='' runtime=''
    # shellcheck disable=SC1090
    source "$path"
}

run_wirecmd_arm() {
    local arm=$1 logs=$2
    local wirecmd=$arm/tools/wirecmd
    [[ -x $wirecmd ]] || { event wirecmd 'missing_client'; return 1; }

    setsid env XDG_RUNTIME_DIR="$runtime" "$wirecmd" daemon run >"$logs/daemon.stdout" 2>"$logs/daemon.stderr" &
    active_daemon_pid=$!
    event daemon "started_pid=$active_daemon_pid"
    wait_for_daemon "$wirecmd" "$runtime" "$logs" || return 1

    if [[ $preparation == warm ]]; then
        capture wirecmd_warm_servers "$logs/warm-servers.json" "$logs/warm-servers.stderr" \
            env XDG_RUNTIME_DIR="$runtime" "$wirecmd" --config "$capability_config" || return 1
        for server in memory remote-tests everything; do
            capture "wirecmd_warm_tools_$server" "$logs/warm-tools-$server.json" "$logs/warm-tools-$server.stderr" \
                env XDG_RUNTIME_DIR="$runtime" "$wirecmd" --config "$capability_config" "$server" || return 1
        done
    fi

    if capture evaluator "$logs/evaluator.stdout" "$logs/evaluator.stderr" \
        "$profile_runner" --codex-bin "$codex_bin" --arm-root "$arm" --model "$model" \
        --allow-network --capability-config "$capability_config"; then
        evaluator_status=0
    else
        evaluator_status=$?
    fi

    if verify_shell_client_transcript "$arm/transcript.jsonl" wirecmd "$logs/shell-transcript-verification.json" "$logs/shell-transcript-verification.stderr"; then
        event wirecmd_parent_verify 'shell_transcript=verified'
    else
        event wirecmd_parent_verify 'shell_transcript=verification_failed'
        evaluator_status=1
    fi

    if capture wirecmd_parent_verify "$logs/parent-memory.json" "$logs/parent-memory.stderr" \
        env XDG_RUNTIME_DIR="$runtime" "$wirecmd" --config "$capability_config" memory read_graph; then
        if empty_graph_file "$logs/parent-memory.json"; then
            event wirecmd_parent_verify 'graph=empty'
        else
            event wirecmd_parent_verify 'graph=nonempty_or_unparseable'
            evaluator_status=1
        fi
    else
        event wirecmd_parent_verify 'graph=unavailable'
        evaluator_status=1
    fi

    return "$evaluator_status"
}

mcpc_command() {
    env HOME="$state/home" MCPC_HOME_DIR="$state" CAPABILITY_CONFIG="$capability_config" "$tools/mcpc" "$@"
}

run_mcpc_arm() {
    local arm=$1 logs=$2
    tools=$arm/tools
    [[ -x $tools/mcpc ]] || { event mcpc 'missing_client'; return 1; }
    mkdir -p -- "$state/home"

    if [[ $preparation == warm ]]; then
        capture mcpc_warm_connect "$logs/warm-connect.json" "$logs/warm-connect.stderr" \
            mcpc_command connect "$capability_config" --stdio --json || return 1
        for session in @memory @remote-tests @everything; do
            capture "mcpc_warm_tools_${session#@}" "$logs/warm-tools-${session#@}.json" "$logs/warm-tools-${session#@}.stderr" \
                mcpc_command "$session" tools-list --json || return 1
        done
    fi

    if capture evaluator "$logs/evaluator.stdout" "$logs/evaluator.stderr" \
        "$profile_runner" --codex-bin "$codex_bin" --arm-root "$arm" --model "$model" \
        --allow-network --capability-config "$capability_config"; then
        evaluator_status=0
    else
        evaluator_status=$?
    fi

    if verify_shell_client_transcript "$arm/transcript.jsonl" mcpc "$logs/shell-transcript-verification.json" "$logs/shell-transcript-verification.stderr"; then
        event mcpc_parent_verify 'shell_transcript=verified'
    else
        event mcpc_parent_verify 'shell_transcript=verification_failed'
        evaluator_status=1
    fi

    if capture mcpc_parent_verify "$logs/parent-memory.json" "$logs/parent-memory.stderr" \
        mcpc_command @memory tools-call read_graph '{}' --json; then
        if empty_graph_file "$logs/parent-memory.json"; then
            event mcpc_parent_verify 'graph=empty'
        else
            event mcpc_parent_verify 'graph=nonempty_or_unparseable'
            evaluator_status=1
        fi
    else
        event mcpc_parent_verify 'graph=unavailable'
        evaluator_status=1
    fi

    return "$evaluator_status"
}

run_native_arm() {
    local arm=$1 logs=$2
    active_native_root=$native_mcp_root
    if capture evaluator "$logs/evaluator.stdout" "$logs/evaluator.stderr" \
        "$profile_runner" --codex-bin "$codex_bin" --arm-root "$arm" --model "$model" \
        --allow-network --native-mcp-root "$native_mcp_root" \
        --mcp-stdio "$native_stdio_memory" --mcp-stdio "$native_stdio_everything" \
        --mcp-http "$native_http"; then
        evaluator_status=0
    else
        evaluator_status=$?
    fi

    if verify_native_transcript "$arm/transcript.jsonl" "$logs/native-parent-verification.json" "$logs/native-parent-verification.stderr"; then
        event native_parent_verify 'transcript=verified'
    else
        event native_parent_verify 'transcript=verification_failed'
        evaluator_status=1
    fi
    return "$evaluator_status"
}

verify_native_transcript() {
    local transcript=$1 evidence=$2 stderr=$3
    [[ -s $transcript ]] || return 1
    jq -s -e '
        [.[] | select(
            .type == "item.completed"
            and (.item.type == "mcp_tool_call" or .item.type == "command_execution")
        ) | .item] | to_entries as $events
        | [
            $events[] as $source
            | select(
                $source.value.type == "mcp_tool_call"
                and $source.value.status == "completed"
                and $source.value.server == "remote-tests"
                and $source.value.tool != "test_x_mcp_header"
            )
            | (($source.value.result.content // [])[]?
                | select(.type == "text" and (.text | type) == "string")
                | .text) as $sentence
            | select(($sentence | length) > 0)
            | ($sentence | ascii_downcase | gsub("[^a-z0-9]"; "")) as $identifier
            | select(($identifier | length) > 0)
            | ($events[] | select(
                .key > $source.key
                and .value.type == "mcp_tool_call"
                and .value.status == "completed"
                and .value.server == "memory"
                and .value.tool == "create_entities"
                and ((.value.arguments.entities // []) | any(.[];
                    .name == $identifier and .observations == [$sentence]
                    and (.entityType | type) == "string" and (.entityType | length) > 0
                ))
            )) as $create
            | (($create.value.arguments.entities // []) | map(select(
                .name == $identifier and .observations == [$sentence]
                and (.entityType | type) == "string" and (.entityType | length) > 0
            ))[0].entityType) as $entity_type
            | select(($create.value.result.structured_content.entities // []) | any(.[];
                .name == $identifier and .entityType == $entity_type and .observations == [$sentence]
            ))
            | ($events[] | select(
                .key > $source.key and .key < $create.key
                and .value.type == "command_execution"
                and .value.status == "completed" and .value.exit_code == 0
                and ((.value.aggregated_output // "") | contains($identifier))
            )) as $transform
            | ($events[] | select(
                .key > $create.key
                and .value.type == "mcp_tool_call"
                and .value.status == "completed"
                and .value.server == "memory" and .value.tool == "open_nodes"
                and .value.arguments.names == [$identifier]
                and ((.value.result.structured_content.entities // []) | any(.[];
                    .name == $identifier and .entityType == $entity_type and .observations == [$sentence]
                ))
            )) as $retrieve
            | ($events[] | select(
                .key > $retrieve.key
                and .value.type == "mcp_tool_call"
                and .value.status == "failed"
                and .value.server == "remote-tests" and .value.tool == "test_x_mcp_header"
                and (.value.arguments.region | type == "number" and floor == .)
            )) as $header_failure
            | ($events[] | select(
                .key > $header_failure.key
                and .value.type == "mcp_tool_call"
                and .value.status == "completed"
                and .value.server == "remote-tests" and .value.tool == "test_x_mcp_header"
                and .value.arguments.region == "EU"
                and (((.value.result.content // []) | map(select(.type == "text") | .text) | index("region=EU")) != null)
            )) as $header_success
            | ($events[] | select(
                .key > $header_success.key
                and .value.type == "mcp_tool_call"
                and .value.status == "completed"
                and .value.server == "memory" and .value.tool == "delete_entities"
                and .value.arguments.entityNames == [$identifier]
            )) as $delete
            | ($events[] | select(
                .key >= $delete.key
                and .value.type == "mcp_tool_call"
                and .value.status == "completed"
                and .value.server == "memory" and .value.tool == "open_nodes"
                and .value.arguments.names == [$identifier]
                and .value.result.structured_content.entities == null
                and .value.result.structured_content.relations == null
            )) as $cleanup
            | {
                source_tool: $source.value.tool,
                sentence: $sentence,
                identifier: $identifier,
                entity_type: $entity_type,
                indexes: {
                    source: $source.key,
                    transform: $transform.key,
                    create: $create.key,
                    retrieve: $retrieve.key,
                    integer_header_failure: $header_failure.key,
                    eu_header_success: $header_success.key,
                    delete: $delete.key,
                    cleanup: $cleanup.key
                }
            }
        ] as $matches
        | if ($matches | length) > 0 then
              $matches[0] | {verified: true, source_tool, sentence, identifier, entity_type, call_indexes: .indexes}
          else error("required native MCP call sequence was not present in the parent-visible transcript")
          end
    ' "$transcript" >"$evidence" 2>"$stderr"
}

verify_shell_client_transcript() {
    local transcript=$1 client=$2 evidence=$3 stderr=$4
    local sentence='This is a simple text response for testing.'
    local identifier='thisisasimpletextresponsefortesting'
    [[ -s $transcript ]] || return 1
    jq -s -e --arg client "$client" --arg sentence "$sentence" --arg identifier "$identifier" '
        [.[] | select(
            .type == "item.completed" and .item.type == "command_execution"
            and .item.status == "completed"
        ) | .item] as $commands
        | ($commands | to_entries | map(select(
            (.value.command | contains($client))
            and .value.exit_code == 0
            and (if $client == "wirecmd"
                 then (.value.command | contains("remote-tests"))
                      and (((.value.command | contains("--help"))
                           or ((.value.aggregated_output // "") | contains("\"tools\""))))
                 else (.value.command | contains("@remote-tests"))
                      and (((.value.command | contains("tools-list"))
                           or (.value.command | contains("tools-get"))
                           or (.value.command | contains("grep"))))
                 end)
        ))) as $discovery
        | ($commands | to_entries | map(select(
            (.value.command | contains($client))
            and .value.exit_code == 0
            and (.value.command | contains("test_simple_text"))
            and (.value.command | contains("create_entities"))
            and ((.value.aggregated_output // "") | contains($sentence))
            and ((.value.aggregated_output // "") | contains($identifier))
            and ((.value.aggregated_output // "") | contains("entityType"))
            and ((.value.aggregated_output // "") | contains("evaluation"))
            and ((.value.aggregated_output // "") | contains("Entities created successfully"))
        ))) as $composed
        | ($commands | to_entries | map(select(
            (.value.command | contains($client))
            and ((.value.command | contains("open_nodes")) or (.value.command | contains("read_graph")))
            and ((.value.aggregated_output // "") | contains($identifier))
            and ((.value.aggregated_output // "") | contains("entityType"))
            and ((.value.aggregated_output // "") | contains("evaluation"))
            and ((.value.aggregated_output // "") | contains($sentence))
        ))) as $retrieve
        | ($commands | to_entries | map(select(
            (.value.command | contains($client))
            and (.value.command | contains("test_x_mcp_header"))
            and (
                (
                    ((.value.aggregated_output // "") | test("validating"; "i"))
                    and ((.value.aggregated_output // "") | test("type.*integer"; "i"))
                    and ((.value.aggregated_output // "") | test("want.*string"; "i"))
                )
                or (
                    $client == "wirecmd"
                    and (.value.command | test("region.*[0-9]"; "i"))
                    and ((.value.aggregated_output // "") | contains("upstream_tool"))
                    and ((.value.aggregated_output // "") | test("exit_status\"?\\s*:\\s*5"))
                )
            )
        ))) as $header_failure
        | ($commands | to_entries | map(select(
            (.value.command | contains($client))
            and .value.exit_code == 0
            and (.value.command | contains("test_x_mcp_header"))
            and (.value.command | contains("EU"))
            and ((.value.aggregated_output // "") | contains("region=EU"))
        ))) as $header_success
        | ($commands | to_entries | map(select(
            (.value.command | contains($client))
            and (.value.command | contains("delete_entities"))
            and ((.value.aggregated_output // "") | contains("Entities deleted successfully"))
        ))) as $delete
        | ($commands | to_entries | map(select(
            (.value.command | contains($client))
            and .value.exit_code == 0
            and (((.value.command | contains("open_nodes")) or (.value.command | contains("read_graph"))))
            and ((.value.aggregated_output // "") | test("entities\"?\\s*:\\s*null"))
            and ((.value.aggregated_output // "") | test("relations\"?\\s*:\\s*null"))
        ))) as $cleanup
        | if [
              (($composed | length) > 0),
              (($discovery | length) > 0),
              (($retrieve | length) > 0),
              (($header_failure | length) > 0),
              (($header_success | length) > 0),
              (($delete | length) > 0),
              (($cleanup | length) > 0),
              ($discovery[0].key < $composed[0].key),
              ($composed[0].key < $retrieve[0].key),
              ($retrieve[0].key <= $header_failure[0].key),
              ($header_failure[0].key <= $header_success[0].key),
              ($header_success[0].key <= $delete[0].key),
              ($delete[0].key <= $cleanup[0].key)
          ] | all
          then {
              verified: true,
              client: $client,
              sentence: $sentence,
              identifier: $identifier,
              command_indexes: {
                  discovery: $discovery[0].key,
                  composed_create: $composed[0].key,
                  retrieve: $retrieve[0].key,
                  integer_header_failure: $header_failure[0].key,
                  eu_header_success: $header_success[0].key,
                  delete: $delete[0].key,
                  empty_retrieval: $cleanup[0].key
              }
          }
          else error("required shell-client command sequence was not present in the parent-visible transcript")
          end
    ' "$transcript" >"$evidence" 2>"$stderr"
}

close_mcpc_sessions() {
    local arm=$1 logs=$2 close_status=0 session pid cmdline
    tools=$arm/tools
    for session in @memory @remote-tests @everything; do
        if ! capture "mcpc_close_${session#@}" "$logs/close-${session#@}.json" "$logs/close-${session#@}.stderr" \
            mcpc_command close "$session"; then
            close_status=1
        fi
    done
    if capture mcpc_parent_sessions "$logs/parent-sessions.json" "$logs/parent-sessions.stderr" mcpc_command --json; then
        grep -Fq '"sessions":[]' "$logs/parent-sessions.json" || close_status=1
    else
        close_status=1
    fi
    while IFS= read -r pid; do
        [[ $pid == "$$" || $pid == "$BASHPID" ]] && continue
        cmdline=$(tr '\0' ' ' <"/proc/$pid/cmdline" 2>/dev/null || true)
        [[ $cmdline == *"pgrep -f -- $tools/"* ]] && continue
        [[ $cmdline == *"$tools/"* ]] || continue
        printf '%s\t%s\n' "$pid" "$cmdline" >>"$logs/remaining-tool-processes.tsv"
        close_status=1
    done < <(pgrep -f -- "$tools/" || true)
    return "$close_status"
}

run_arm() {
    local sequence=$1 id=$2 expected_condition=$3 expected_preparation=$4 expected_repetition=$5
    local arm=$run_root/arms/$id logs sibling_config blocked_parent plan_sequence=$sequence plan_condition=$expected_condition plan_preparation=$expected_preparation plan_repetition=$expected_repetition
    [[ -d $arm && -d $arm/parent/logs ]] || fail "missing staged arm: $id"
    [[ ! -e $arm/transcript.jsonl && ! -e $arm/stderr.log && ! -e $arm/run.meta ]] || fail "arm already has evaluator artifacts: $id"
    current_parent=$arm/parent
    : >"$current_parent/lifecycle.tsv"
    source_arm_env "$current_parent/arm.env"
    [[ $sequence == "$plan_sequence" && $condition == "$plan_condition" && $preparation == "$plan_preparation" && $repetition == "$plan_repetition" && $run_id == "$id" ]] || fail "plan and parent metadata differ for $id"
    runtime=$arm/runtime
    state=$arm/state
    logs=$current_parent/logs
    event arm "sequence=$sequence id=$id condition=$condition preparation=$preparation repetition=$repetition"

    sibling_config=$(find "$run_root/arms" -mindepth 3 -maxdepth 3 -type f -path '*/inputs/config' ! -path "$arm/inputs/config" -print -quit)
    [[ -n $sibling_config ]] || fail "no sibling staged configuration is available for isolation preflight"
    if [[ $condition == native ]]; then
        blocked_parent=$native_mcp_root/memory-mcp
    else
        blocked_parent=$current_parent/arm.env
    fi
    if ! capture profile_preflight "$logs/profile-preflight.stdout" "$logs/profile-preflight.stderr" \
        "$profile_preflight" --codex-bin "$codex_bin" --arm-root "$arm" \
        --blocked-path "$repo_root/go.mod" --blocked-path "$sibling_config" --blocked-path "$blocked_parent"; then
        event profile_preflight 'status=failed'
        current_parent=
        return 1
    fi
    event profile_preflight 'status=passed'

    setsid "$conformance_binary" -http "127.0.0.1:$http_port" -stateless=true >"$logs/conformance.stdout" 2>"$logs/conformance.stderr" &
    active_conformance_pid=$!
    event conformance "started_pid=$active_conformance_pid port=$http_port"
    if ! wait_for_conformance "$http_url" "$logs"; then
        cleanup_active
        current_parent=
        return 1
    fi

    local status=0
    case $condition in
        wirecmd) run_wirecmd_arm "$arm" "$logs" || status=1 ;;
        mcpc) run_mcpc_arm "$arm" "$logs" || status=1 ;;
        native) run_native_arm "$arm" "$logs" || status=1 ;;
        *) fail "unknown condition in staged metadata: $condition" ;;
    esac
    if [[ $condition == mcpc ]]; then
        close_mcpc_sessions "$arm" "$logs" || status=1
    fi
    cleanup_active
    if [[ $condition == wirecmd ]]; then
        if [[ -S $runtime/wirecmd/daemon.sock ]]; then
            event daemon_cleanup 'socket=remaining'
            status=1
        else
            event daemon_cleanup 'socket=removed'
        fi
    fi
    event arm "complete status=$status"
    current_parent=
    return "$status"
}

successes=0
failures=0
while IFS=$'\t' read -r sequence id condition preparation repetition; do
    [[ $sequence != sequence ]] || continue
    if run_arm "$sequence" "$id" "$condition" "$preparation" "$repetition"; then
        successes=$((successes + 1))
    else
        failures=$((failures + 1))
    fi
done <"$plan"

printf 'runs=%s successes=%s failures=%s\n' "$((successes + failures))" "$successes" "$failures" | tee "$run_root/parent/execution-summary.txt"
((failures == 0))
