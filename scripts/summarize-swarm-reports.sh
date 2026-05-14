#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -eq 0 ]; then
  echo "usage: $0 <swarm-report.json>..." >&2
  exit 1
fi

printf "%-8s %-8s %-14s %-17s %-14s %-14s %-14s %-14s\n" \
  "agents" "rounds" "vm_p50_ms" "ready_p50_ms" "msg_p50_ms" "msg_p95_ms" "round_p50_ms" "round_p95_ms"

if command -v jq >/dev/null 2>&1; then
  for report in "$@"; do
    jq -r '[
      .agents,
      .rounds,
      (.summary.vm_start_p50_ms | tostring),
      (.summary.agent_ready_p50_ms | tostring),
      (.summary.message_p50_ms | tostring),
      (.summary.message_p95_ms | tostring),
      (.summary.round_p50_ms | tostring),
      (.summary.round_p95_ms | tostring)
    ] | @tsv' "$report"
  done
elif command -v python3 >/dev/null 2>&1; then
  python3 - "$@" <<'PY'
import json
import sys

for path in sys.argv[1:]:
    with open(path, "r", encoding="utf-8") as f:
        data = json.load(f)
    summary = data["summary"]
    print("\t".join(str(x) for x in [
        data["agents"],
        data["rounds"],
        summary["vm_start_p50_ms"],
        summary["agent_ready_p50_ms"],
        summary["message_p50_ms"],
        summary["message_p95_ms"],
        summary["round_p50_ms"],
        summary["round_p95_ms"],
    ]))
PY
else
  echo "jq or python3 is required to summarize swarm reports" >&2
  exit 1
fi | sort -n | while IFS=$'\t' read -r agents rounds vm ready msg50 msg95 round50 round95; do
  printf "%-8s %-8s %-14s %-17s %-14s %-14s %-14s %-14s\n" \
    "$agents" "$rounds" "$vm" "$ready" "$msg50" "$msg95" "$round50" "$round95"
done
