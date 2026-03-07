#!/usr/bin/env bash
# connectivity-check.sh — Full connectivity diagnostic between a source pod and a service.
# Usage: connectivity-check.sh <source-pod> <service> [-n namespace] [-p port]
#
# Runs: trace → connectivity → dns → netpol on both ends.

set -euo pipefail

SRC=""
DST=""
NAMESPACE_FLAG=""
PORT_FLAG=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    -n|--namespace) NAMESPACE_FLAG="-n $2"; shift 2 ;;
    -p|--port)      PORT_FLAG="-p $2"; shift 2 ;;
    -*)             echo "Unknown flag: $1" >&2; exit 1 ;;
    *)
      if [[ -z "$SRC" ]]; then SRC="$1"
      elif [[ -z "$DST" ]]; then DST="$1"
      else echo "Too many arguments" >&2; exit 1
      fi
      shift ;;
  esac
done

if [[ -z "$SRC" || -z "$DST" ]]; then
  echo "Usage: connectivity-check.sh <source-pod> <service> [-n namespace] [-p port]" >&2
  exit 1
fi

FLAGS="$NAMESPACE_FLAG"

echo "=== Step 1: Trace network path ==="
kdiag trace "$SRC" "$DST" $FLAGS || true

echo ""
echo "=== Step 2: Test connectivity ==="
kdiag connectivity "$SRC" "$DST" $FLAGS $PORT_FLAG || true

echo ""
echo "=== Step 3: DNS resolution ==="
kdiag dns "$DST" $FLAGS || true

echo ""
echo "=== Step 4: Network policies on source ==="
kdiag netpol "$SRC" $FLAGS || true
