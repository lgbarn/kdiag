#!/usr/bin/env bash
# connectivity-check.sh — Full connectivity diagnostic between a source pod and a service.
# Usage: connectivity-check.sh <source-pod> <service> [-n namespace] [-p port]
#
# Runs: trace → connectivity → dns → netpol on both ends.

set -euo pipefail

SRC=""
DST=""
NS_FLAGS=()
PORT_FLAGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    -n|--namespace) NS_FLAGS+=("-n" "$2"); shift 2 ;;
    -p|--port)      PORT_FLAGS+=("-p" "$2"); shift 2 ;;
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

echo "=== Step 1: Trace network path ==="
kdiag trace "$SRC" "$DST" "${NS_FLAGS[@]}" || true

echo ""
echo "=== Step 2: Test connectivity ==="
kdiag connectivity "$SRC" "$DST" "${NS_FLAGS[@]}" "${PORT_FLAGS[@]}" || true

echo ""
echo "=== Step 3: DNS resolution ==="
kdiag dns "$DST" "${NS_FLAGS[@]}" || true

echo ""
echo "=== Step 4: Network policies on source ==="
kdiag netpol "$SRC" "${NS_FLAGS[@]}" || true
