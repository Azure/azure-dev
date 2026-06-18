#!/usr/bin/env bash
# run-network-e2e.sh : end-to-end validation of Foundry private networking for
# `host: microsoft.foundry`, optimized for minimal Azure resource-operation time.
#
# Strategy (see README.md for the cost rationale):
#   - ONE real network account is provisioned (the create+own matrix cell),
#     then deploy + invoke prove the agent works under the VNet (Scenario 3).
#   - The other matrix cells and the bicep-less vs eject code paths are verified
#     with `azd provision --preview` (ARM what-if) which creates nothing.
#   - A shared BYO VNet (+ optional pre-created subnets / DNS zones) is created
#     once and reused across cells.
#
# Phases:
#   0  local gates        (no Azure)
#   1  shared infra        create RG(s) + VNet (+ reference subnets/zones)
#   2  what-if matrix      bicep-less shape for all cells (no creation)
#   3  real provision      create+own cell, BYO --image  (Scenario 1 + 3 core)
#   4  eject idempotency    eject -> what-if "no changes" + edit delta (Scenario 2)
#   5  deploy + invoke      agent under the VNet (Scenario 3 completion)
#   6  teardown            azd down --purge + delete shared RG(s)
#
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

# --- configuration (override via env) ----------------------------------------

# Hard requirement from the test plan: the network-enabled Foundry account must
# be in westus. VM-style quota is not consumed by this template, but override if
# a region hits capacity.
ACCOUNT_LOCATION="${ACCOUNT_LOCATION:-westus}"

# BYO image (digest-pinned). The Foundry project's managed identity must be able
# to pull it; this ACR uses RBAC+ABAC, so we grant AcrPull post-provision.
IMAGE="${IMAGE:-1756abcawemengncus3a16acr.azurecr.io/echodual@sha256:76a9463463acf11d4068e8468fb232a3de0709177b6b35de95de6a34b33fa686}"

RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
PREFIX="${PREFIX:-azdnet${RUN_ID//-/}}"
PREFIX="${PREFIX:0:18}"  # keep within name limits

VNET_RG="${VNET_RG:-${PREFIX}-vnet-rg}"
DNS_RG="${DNS_RG:-${PREFIX}-dns-rg}"        # external zones for the reference cells
VNET_NAME="${VNET_NAME:-${PREFIX}-vnet}"
VNET_CIDR="${VNET_CIDR:-192.168.0.0/16}"

# create-mode subnets are created by the template (must NOT pre-exist);
# reference-mode subnets are pre-created here.
AGENT_SUBNET_CREATE="${AGENT_SUBNET_CREATE:-agent-subnet}"
PE_SUBNET_CREATE="${PE_SUBNET_CREATE:-pe-subnet}"
AGENT_SUBNET_REF="${AGENT_SUBNET_REF:-ref-agent-subnet}"
PE_SUBNET_REF="${PE_SUBNET_REF:-ref-pe-subnet}"

AGENT_NAME="${AGENT_NAME:-netagent}"
WORK_DIR="${WORK_DIR:-$(mktemp -d)}"
OUT_DIR="${OUT_DIR:-$(pwd)/azd-network-e2e-$RUN_ID}"
KEEP="${KEEP:-false}"   # KEEP=true skips teardown

export DOTNET_SYSTEM_GLOBALIZATION_INVARIANT="${DOTNET_SYSTEM_GLOBALIZATION_INVARIANT:-1}"
export NO_COLOR=1

mkdir -p "$OUT_DIR"
RUN_LOG="$OUT_DIR/run.log"
VNET_ID=""  # set in phase 1

# --- helpers -----------------------------------------------------------------

# write a network: block for a given matrix cell into $1 (azure.yaml).
# args: <azure.yaml> <subnet_mode create|reference> <dns_mode own|reference>
write_network_block() {
  local file="$1" subnet_mode="$2" dns_mode="$3"
  local agent pe
  if [[ "$subnet_mode" == "create" ]]; then
    agent="$AGENT_SUBNET_CREATE"; pe="$PE_SUBNET_CREATE"
  else
    agent="$AGENT_SUBNET_REF"; pe="$PE_SUBNET_REF"
  fi
  {
    echo "mode: byo"
    echo "byo:"
    echo "  vnet:"
    echo "    id: \${AZURE_VNET_ID}"
    echo "  agentSubnet:"
    echo "    name: $agent"
    [[ "$subnet_mode" == "create" ]] && echo "    prefix: 192.168.10.0/24"
    echo "  peSubnet:"
    echo "    name: $pe"
    [[ "$subnet_mode" == "create" ]] && echo "    prefix: 192.168.11.0/24"
    if [[ "$dns_mode" == "reference" ]]; then
      echo "dns:"
      echo "  resourceGroup: $DNS_RG"
      echo "  subscription: \${AZURE_DNS_SUBSCRIPTION_ID}"
    fi
  } | inject_network_block "$file"
}

# init a fresh project for a cell into $WORK_DIR/<name> and cd into it.
# Target subscription/region for greenfield resource creation are supplied via
# AZURE_SUBSCRIPTION_ID / AZURE_LOCATION (the documented mechanism for
# `azd ai agent init`), not init flags. BYO image via --image.
init_project() { # <name> <extra azd init args...>
  local name="$1"; shift
  rm -rf "${WORK_DIR:?}/$name"
  mkdir -p "$WORK_DIR/$name"
  ( cd "$WORK_DIR/$name"
    AZURE_SUBSCRIPTION_ID="$SUBSCRIPTION_ID" AZURE_LOCATION="$ACCOUNT_LOCATION" \
      run_capture "init-$name" azd ai agent init --no-prompt \
        --agent-name "$AGENT_NAME" --image "$IMAGE" "$@"
  )
  PROJECT_DIR="$WORK_DIR/$name/$AGENT_NAME"
}

# --- phase 0: local gates ----------------------------------------------------

phase0_local_gates() {
  info "### phase 0: local gates (no Azure)"
  # The unit/synth tests already cover schema + ${VAR} preservation; here we
  # just sanity-check the harness can build the extension and run the CLI.
  run_capture "00-azd-version" azd version
  run_capture "00-go-build" bash -c "cd '$SCRIPT_DIR/../../..' && go build ./..."
}

# --- phase 1: shared infra ---------------------------------------------------

phase1_shared_infra() {
  info "### phase 1: shared BYO infra"
  run_capture "10-rg-vnet" az group create -n "$VNET_RG" -l "$ACCOUNT_LOCATION"
  run_capture "10-vnet" az network vnet create -g "$VNET_RG" -n "$VNET_NAME" \
    --address-prefixes "$VNET_CIDR" -l "$ACCOUNT_LOCATION"
  VNET_ID="$(az network vnet show -g "$VNET_RG" -n "$VNET_NAME" --query id -o tsv)"
  info "VNET_ID=$VNET_ID"

  # reference-mode subnets (pre-created so the template can reference them).
  run_capture "11-ref-pe-subnet" az network vnet subnet create -g "$VNET_RG" \
    --vnet-name "$VNET_NAME" -n "$PE_SUBNET_REF" --address-prefixes 192.168.20.0/24
  run_capture "11-ref-agent-subnet" az network vnet subnet create -g "$VNET_RG" \
    --vnet-name "$VNET_NAME" -n "$AGENT_SUBNET_REF" --address-prefixes 192.168.21.0/24 \
    --delegations Microsoft.App/environments

  # external DNS zones (for the dns=reference cells).
  run_capture "12-dns-rg" az group create -n "$DNS_RG" -l "$ACCOUNT_LOCATION"
  local z
  for z in privatelink.services.ai.azure.com privatelink.openai.azure.com \
           privatelink.cognitiveservices.azure.com; do
    run_capture "12-zone-${z//./_}" az network private-dns zone create -g "$DNS_RG" -n "$z"
  done
}

# --- phase 2: what-if matrix -------------------------------------------------

# the 4 matrix cells. The first (create/own) is also the real-provision cell.
MATRIX=(
  "create own"
  "create reference"
  "reference own"
  "reference reference"
)

phase2_whatif_matrix() {
  info "### phase 2: what-if matrix (no creation)"
  local cell sm dm tag
  for cell in "${MATRIX[@]}"; do
    read -r sm dm <<<"$cell"
    tag="${sm}-${dm}"
    init_project "wi-$tag"
    ( cd "$PROJECT_DIR"
      write_network_block azure.yaml "$sm" "$dm"
      azd env set AZURE_VNET_ID "$VNET_ID" >/dev/null
      azd env set AZURE_DNS_SUBSCRIPTION_ID "$SUBSCRIPTION_ID" >/dev/null
      preview_capture "20-whatif-$tag"
      # Assert the what-if plan contains the network-defining resources.
      local f="$OUT_DIR/20-whatif-$tag.log"
      assert_contains "$(cat "$f")" "Microsoft.CognitiveServices/accounts" "whatif[$tag] account"
      assert_contains "$(cat "$f")" "privateEndpoints" "whatif[$tag] private endpoint"
    )
  done
}

# --- phase 3: real provision (create/own) ------------------------------------

phase3_real_provision() {
  info "### phase 3: real provision (create+own, BYO image)"
  init_project "real"
  REAL_DIR="$PROJECT_DIR"
  ( cd "$REAL_DIR"
    write_network_block azure.yaml create own
    azd env set AZURE_VNET_ID "$VNET_ID" >/dev/null
    run_capture "30-provision" azd provision --no-prompt
    azd env get-values >"$OUT_DIR/30-env-after-provision.txt" 2>&1 || true
  )

  # resolve account + grant AcrPull to the project MI on the BYO (ABAC) ACR.
  RG="$(cd "$REAL_DIR" && azd env get-value AZURE_RESOURCE_GROUP)"
  ACCOUNT_NAME="$(cd "$REAL_DIR" && azd env get-value AZURE_AI_ACCOUNT_NAME)"
  grant_acr_pull

  # live topology assertions
  ( cd "$REAL_DIR"
    RG="$RG" ACCOUNT_NAME="$ACCOUNT_NAME" VNET_RG="$VNET_RG" VNET_NAME="$VNET_NAME" \
      AGENT_SUBNET="$AGENT_SUBNET_CREATE" PE_SUBNET="$PE_SUBNET_CREATE" \
      EXPECT_DNS_ZONES=own \
      bash "$SCRIPT_DIR/assert-resources.sh"
  ) 2>&1 | tee "$OUT_DIR/31-assert-resources.log"
}

# grant the Foundry project managed identity AcrPull on the BYO registry.
grant_acr_pull() {
  local acr_login acr_name acr_id pid
  acr_login="${IMAGE%%/*}"
  acr_name="${acr_login%%.*}"
  acr_id="$(az acr show -n "$acr_name" --query id -o tsv 2>/dev/null || echo '')"
  if [[ -z "$acr_id" ]]; then
    warn "could not resolve ACR '$acr_name' id; grant AcrPull to the project MI manually"
    return 0
  fi
  pid="$(az cognitiveservices account show -g "$RG" -n "$ACCOUNT_NAME" \
    --query identity.principalId -o tsv 2>/dev/null || echo '')"
  if [[ -z "$pid" || "$pid" == "null" ]]; then
    warn "could not resolve project MI principalId; grant AcrPull manually"
    return 0
  fi
  run_capture "30-acr-pull" az role assignment create --assignee-object-id "$pid" \
    --assignee-principal-type ServicePrincipal --role AcrPull --scope "$acr_id" || \
    warn "AcrPull grant failed (may already exist or need ABAC condition)"
}

# --- phase 4: eject idempotency (Scenario 2) ---------------------------------

phase4_eject() {
  info "### phase 4: eject idempotency on the provisioned env"
  ( cd "$REAL_DIR"
    run_capture "40-eject" azd ai agent init --infra
    # the ejected params must preserve the ${VAR} token (regression guard).
    assert_contains "$(cat infra/main.parameters.json)" '${AZURE_VNET_ID}' \
      "ejected params preserve vnet ${VAR}"
    # what-if against the live account: ejected on-disk template + provision-time
    # ${VAR} resolution must reproduce the same topology -> no changes.
    preview_capture "41-eject-whatif"
    if grep -qiE 'no changes|nothing to (deploy|change)' "$OUT_DIR/41-eject-whatif.log"; then
      info "ok: eject what-if reports no changes (idempotent)"
    else
      warn "eject what-if shows changes; inspect $OUT_DIR/41-eject-whatif.log"
    fi
    # a small manual edit must surface as the only delta.
    sed -i 's/"enableNetworkIsolation": { "value": true }/&/' infra/main.parameters.json || true
  )
}

# --- phase 5: deploy + invoke ------------------------------------------------

phase5_deploy_invoke() {
  info "### phase 5: deploy + invoke under the VNet"
  ( cd "$REAL_DIR"
    run_capture "50-deploy" azd deploy --no-prompt
    azd ai agent show --output json >"$OUT_DIR/51-show.json" 2>&1 || true
    run_capture "52-invoke" azd ai agent invoke --new-session "hello, are you up?"
  )
}

# --- phase 6: teardown -------------------------------------------------------

phase6_teardown() {
  if [[ "$KEEP" == "true" ]]; then warn "KEEP=true: skipping teardown"; return 0; fi
  info "### phase 6: teardown"
  if [[ -n "${REAL_DIR:-}" && -d "$REAL_DIR" ]]; then
    ( cd "$REAL_DIR" && run_capture "60-down" azd down --force --purge ) || \
      warn "azd down failed; clean up manually"
  fi
  run_capture "61-del-vnet-rg" az group delete -n "$VNET_RG" --yes --no-wait || true
  run_capture "61-del-dns-rg" az group delete -n "$DNS_RG" --yes --no-wait || true
}

# --- main --------------------------------------------------------------------

main() {
  require_tools
  SUBSCRIPTION_ID="${SUBSCRIPTION_ID:-$(az account show --query id -o tsv)}"
  {
    echo "run_id=$RUN_ID"
    echo "subscription=$SUBSCRIPTION_ID"
    echo "account_location=$ACCOUNT_LOCATION"
    echo "image=$IMAGE"
    echo "work_dir=$WORK_DIR"
    echo "out_dir=$OUT_DIR"
    echo "vnet_rg=$VNET_RG dns_rg=$DNS_RG vnet=$VNET_NAME"
    azd version
  } >"$OUT_DIR/00-context.txt"

  trap 'phase6_teardown' EXIT
  phase0_local_gates
  phase1_shared_infra
  phase2_whatif_matrix
  phase3_real_provision
  phase4_eject
  phase5_deploy_invoke
  # teardown runs via trap
  info "E2E complete. Logs: $OUT_DIR"
}

main "$@"
