#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work_dir="$(mktemp -d)"
runtime_dir="$work_dir/runtime"
http_log="$work_dir/legacy-http.stderr"
stdio_protocol="$work_dir/legacy-stdio-protocol.jsonl"
http_protocol="$work_dir/legacy-http-protocol.jsonl"
daemon_pid=""
fixture_pid=""

cleanup() {
  if [[ -n "$daemon_pid" ]]; then
    kill -TERM "$daemon_pid" 2>/dev/null || true
    wait "$daemon_pid" 2>/dev/null || true
  fi
  if [[ -n "$fixture_pid" ]]; then
    kill -TERM "$fixture_pid" 2>/dev/null || true
    wait "$fixture_pid" 2>/dev/null || true
  fi
  rm -rf -- "$work_dir"
}
trap cleanup EXIT

command -v go >/dev/null
command -v jq >/dev/null

go build -buildvcs=false -o "$work_dir/wirecmd" "$repo_root"
(
  cd "$repo_root/testdata/legacy-mcp"
  go build -buildvcs=false -o "$work_dir/legacy-mcp" .
)

"$work_dir/legacy-mcp" \
  --listen 127.0.0.1:0 \
  --protocol-record "$http_protocol" \
  2>"$http_log" &
fixture_pid=$!

endpoint=""
for _ in {1..100}; do
  endpoint="$(sed -n 's/^legacy-mcp listening: //p' "$http_log")"
  [[ -n "$endpoint" ]] && break
  kill -0 "$fixture_pid" 2>/dev/null || {
    sed -n '1,120p' "$http_log" >&2
    exit 1
  }
  sleep 0.05
done
[[ -n "$endpoint" ]] || {
  echo "legacy fixture did not become ready" >&2
  exit 1
}

cat >"$work_dir/wirecmd.kdl" <<EOF
wirecmd {
    mcp "legacy-stdio" {
        scope "workspace"
        stdio "$work_dir/legacy-mcp" {
            arg "--protocol-record"
            arg "$stdio_protocol"
        }
    }
    mcp "legacy-http" {
        scope "workspace"
        http "$endpoint"
    }
}
EOF

mkdir "$runtime_dir"
chmod 700 "$runtime_dir"
XDG_RUNTIME_DIR="$runtime_dir" "$work_dir/wirecmd" daemon run \
  >"$work_dir/daemon.stdout" 2>"$work_dir/daemon.stderr" &
daemon_pid=$!

for _ in {1..100}; do
  [[ -S "$runtime_dir/wirecmd/daemon.sock" ]] && break
  kill -0 "$daemon_pid" 2>/dev/null || {
    sed -n '1,120p' "$work_dir/daemon.stderr" >&2
    exit 1
  }
  sleep 0.05
done
[[ -S "$runtime_dir/wirecmd/daemon.sock" ]] || {
  echo "wirecmd daemon did not become ready" >&2
  exit 1
}

wirecmd() {
  XDG_RUNTIME_DIR="$runtime_dir" "$work_dir/wirecmd" \
    --config "$work_dir/wirecmd.kdl" "$@"
}

wirecmd legacy-stdio | jq -e '.ok and any(.tools[]; .name == "set_value")' >/dev/null
wirecmd --help legacy-stdio set_value | grep -F -- '--value' >/dev/null
wirecmd legacy-stdio set_value --value stdio-retained \
  | jq -e '.ok and .result.data.value == "stdio-retained"' >/dev/null
wirecmd legacy-stdio read_value \
  | jq -e '.ok and .result.data.exists and .result.data.value == "stdio-retained"' >/dev/null

wirecmd legacy-http '{"tool":"set_value","arguments":{"value":"http-retained"}}' \
  | jq -e '.ok and .result.data.value == "http-retained"' >/dev/null
wirecmd legacy-http read_value \
  | jq -e '.ok and .result.data.exists and .result.data.value == "http-retained"' >/dev/null

"$work_dir/wirecmd" --direct --config "$work_dir/wirecmd.kdl" legacy-stdio read_value \
  | jq -e '.ok and (.result.data.exists | not)' >/dev/null
"$work_dir/wirecmd" --direct --config "$work_dir/wirecmd.kdl" legacy-http read_value \
  | jq -e '.ok and (.result.data.exists | not)' >/dev/null

set +e
failure="$(wirecmd legacy-http fail)"
failure_exit=$?
set -e
[[ "$failure_exit" -eq 5 ]]
jq -e '.ok == false and .error.category == "upstream_tool" and .error.code == "tool_reported_error"' \
  <<<"$failure" >/dev/null

XDG_RUNTIME_DIR="$runtime_dir" "$work_dir/wirecmd" daemon reload \
  | jq -e '.ok and .reload.instances_retired == 2' >/dev/null
wirecmd legacy-stdio read_value \
  | jq -e '.ok and (.result.data.exists | not)' >/dev/null
wirecmd legacy-http read_value \
  | jq -e '.ok and (.result.data.exists | not)' >/dev/null

[[ -s "$stdio_protocol" && -s "$http_protocol" ]]
jq -e -s 'length > 0 and all(.[]; .protocol_version == "2025-11-25")' \
  "$stdio_protocol" >/dev/null
jq -e -s 'length > 0 and all(.[]; .protocol_version == "2025-11-25")' \
  "$http_protocol" >/dev/null

echo "legacy initialized stdio and Streamable HTTP qualification passed"
