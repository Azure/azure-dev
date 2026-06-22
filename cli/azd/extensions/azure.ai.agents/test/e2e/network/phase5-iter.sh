#!/usr/bin/env bash
# Phase-5 iteration driver: reuse the resources a KEEP=true run already
# provisioned, and exercise only the jumpbox + deploy + invoke path. Not part
# of the harness; a local debugging aid.
set -Eeuo pipefail

export RUN_ID="${RUN_ID:?set RUN_ID of the kept run, e.g. live-0622-165947}"
export SUBSCRIPTION_ID="${SUBSCRIPTION_ID:-1756abc0-3554-4341-8d6a-46674962ea19}"
export ACCOUNT_LOCATION="${ACCOUNT_LOCATION:-westus}"
export CLIENT_LOCATION="${CLIENT_LOCATION:-eastus}"
export WORK_DIR="${WORK_DIR:?set WORK_DIR of the kept run}"
export OUT_DIR="${OUT_DIR:-/tmp/azdnet-p5-$RUN_ID}"
export RUN_DEPLOY=true KEEP=true NO_COLOR=1
mkdir -p "$OUT_DIR"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=run-network-e2e.sh
source "$SCRIPT_DIR/run-network-e2e.sh"   # functions + vars, no main (guarded)

RUN_LOG="$OUT_DIR/run.log"
SUBSCRIPTION_ID="$(az account show --query id -o tsv)"

# Re-bind the runtime vars a normal run sets in phases 1/3.
VNET_ID="$(az network vnet show -g "$VNET_RG" -n "$VNET_NAME" --query id -o tsv)"
REAL_DIR="$WORK_DIR/real"
RG="$(cd "$REAL_DIR" && azd env get-value AZURE_RESOURCE_GROUP)"
ACCOUNT_NAME="$(cd "$REAL_DIR" && azd env get-value AZURE_AI_ACCOUNT_NAME)"
info "phase5-iter: VNET_ID=$VNET_ID RG=$RG ACCOUNT=$ACCOUNT_NAME REAL_DIR=$REAL_DIR"

phase5_deploy_invoke
info "phase5-iter complete; logs in $OUT_DIR"
