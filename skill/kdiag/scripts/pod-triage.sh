#!/usr/bin/env bash
# pod-triage.sh — Run the standard diagnostic sequence for a failing pod.
# Usage: pod-triage.sh <pod-name> [-n namespace] [--profile aws-profile]
#
# Runs: diagnose → inspect → logs (if crashing) in one shot.
# Outputs table format by default; set KDIAG_OUTPUT=json for machine-readable.

set -euo pipefail

POD=""
NAMESPACE_FLAG=""
PROFILE_FLAG=""
OUTPUT_FLAG=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    -n|--namespace)  NAMESPACE_FLAG="-n $2"; shift 2 ;;
    --profile)       PROFILE_FLAG="--profile $2"; shift 2 ;;
    -o|--output)     OUTPUT_FLAG="-o $2"; shift 2 ;;
    -*)              echo "Unknown flag: $1" >&2; exit 1 ;;
    *)               POD="$1"; shift ;;
  esac
done

if [[ -z "$POD" ]]; then
  echo "Usage: pod-triage.sh <pod-name> [-n namespace] [--profile aws-profile]" >&2
  exit 1
fi

FLAGS="$NAMESPACE_FLAG $OUTPUT_FLAG"

echo "=== Step 1: Diagnose ==="
kdiag diagnose "$POD" $FLAGS $PROFILE_FLAG || true

echo ""
echo "=== Step 2: Inspect ==="
kdiag inspect "$POD" $FLAGS

# Check if pod is in CrashLoopBackOff or has restarts
STATUS=$(kdiag inspect "$POD" $NAMESPACE_FLAG -o json 2>/dev/null | \
  python3 -c "import sys,json; d=json.load(sys.stdin); cs=d.get('container_statuses',[]); print('crash' if any(c.get('restart_count',0)>0 for c in cs) else 'ok')" 2>/dev/null || echo "ok")

if [[ "$STATUS" == "crash" ]]; then
  echo ""
  echo "=== Step 3: Logs (restarting detected) ==="
  kdiag logs "$POD" $NAMESPACE_FLAG --previous 2>/dev/null || kdiag logs "$POD" $NAMESPACE_FLAG || true
fi
