#!/usr/bin/env bash
# eks-health.sh — Comprehensive EKS cluster health check.
# Usage: eks-health.sh [--profile aws-profile] [--region aws-region]
#
# Runs: health → eks cni → eks node → eks endpoint in sequence.

set -euo pipefail

PROFILE_FLAG=""
REGION_FLAG=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile) PROFILE_FLAG="--profile $2"; shift 2 ;;
    --region)  REGION_FLAG="--region $2"; shift 2 ;;
    -*)        echo "Unknown flag: $1" >&2; exit 1 ;;
    *)         echo "Unexpected argument: $1" >&2; exit 1 ;;
  esac
done

EKS_FLAGS="$PROFILE_FLAG $REGION_FLAG"

echo "=== Step 1: Cluster health overview ==="
kdiag health || true

echo ""
echo "=== Step 2: VPC CNI status ==="
kdiag eks cni $EKS_FLAGS || true

echo ""
echo "=== Step 3: Node ENI/IP capacity ==="
kdiag eks node $EKS_FLAGS || true

echo ""
echo "=== Step 4: VPC endpoint checks ==="
kdiag eks endpoint $EKS_FLAGS || true
