#!/usr/bin/env bash
# pod-triage.sh — Run the standard diagnostic sequence for a failing pod.
# Usage: pod-triage.sh <pod-name> [-n namespace] [--profile aws-profile]
#
# Runs: diagnose → inspect → logs (if crashing) in one shot.
# Outputs table format by default; set -o json for machine-readable.

set -euo pipefail

POD=""
FLAGS=()
NS_FLAGS=()
PROFILE_FLAGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    -n|--namespace)  NS_FLAGS+=("-n" "$2"); shift 2 ;;
    --profile)       PROFILE_FLAGS+=("--profile" "$2"); shift 2 ;;
    -o|--output)     FLAGS+=("-o" "$2"); shift 2 ;;
    -*)              echo "Unknown flag: $1" >&2; exit 1 ;;
    *)               POD="$1"; shift ;;
  esac
done

if [[ -z "$POD" ]]; then
  echo "Usage: pod-triage.sh <pod-name> [-n namespace] [--profile aws-profile]" >&2
  exit 1
fi

echo "=== Step 1: Diagnose ==="
kdiag diagnose "$POD" "${NS_FLAGS[@]}" "${FLAGS[@]}" "${PROFILE_FLAGS[@]}" || true

echo ""
echo "=== Step 2: Inspect ==="
kdiag inspect "$POD" "${NS_FLAGS[@]}" "${FLAGS[@]}"

# Check if pod is in CrashLoopBackOff or has restarts
STATUS=$(timeout 10 kdiag inspect "$POD" "${NS_FLAGS[@]}" -o json 2>/dev/null | \
  python3 -c "import sys,json; d=json.load(sys.stdin); cs=d.get('container_statuses',[]); print('crash' if any(c.get('restart_count',0)>0 for c in cs) else 'ok')" 2>/dev/null || echo "ok")

if [[ "$STATUS" == "crash" ]]; then
  echo ""
  echo "=== Step 3: Logs (restarting detected) ==="
  kdiag logs "$POD" "${NS_FLAGS[@]}" --previous 2>/dev/null || kdiag logs "$POD" "${NS_FLAGS[@]}" || true
fi
