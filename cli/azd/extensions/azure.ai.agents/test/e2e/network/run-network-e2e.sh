#!/usr/bin/env bash
# run-network-e2e.sh : end-to-end validation of Foundry private networking for
# `host: azure.ai.agent`, optimized for minimal Azure resource-operation time.
#
# Strategy (see README.md for the cost rationale):
#   - ONE real network account is provisioned (the create+own matrix cell).
#   - The other matrix cells and the bicep-less vs eject code paths are verified
#     with `azd provision --preview` (ARM what-if) which creates nothing.
#   - A shared BYO VNet (+ optional pre-created subnets / DNS zones) is created
#     once and reused across cells.
#
# Phases 0-4 validate all the *networking* code and do NOT require the BYO-image
# init UX (`azd ai agent init --image`). The project is hand-authored
# (azure.yaml fixture), so it runs against the current branch today. Phase 5
# (deploy + invoke the BYO image under the VNet) uses the deploy-time pre-built
# image short-circuit and is gated behind RUN_DEPLOY=true.
#
# Phases:
#   0  local gates        (no Azure)
#   1  shared infra        create RG(s) + VNet (+ reference subnets/zones)
#   2  what-if matrix      bicep-less shape for all cells (no creation)
#   3  real provision      create+own cell  (Scenario 1 + network topology)
#   4  eject idempotency    eject -> what-if "no changes" + edit delta (Scenario 2)
#   5  deploy + invoke      agent under the VNet (Scenario 3) -- RUN_DEPLOY=true
#   6  teardown            azd down --purge + delete shared RG(s)
#
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"
# shellcheck source=lib-jumpbox.sh
source "$SCRIPT_DIR/lib-jumpbox.sh"

# --- configuration (override via env) ----------------------------------------

# Region constraints (from the test plan):
#  - The network-enabled Foundry account, its VNet, DNS zones, and ACR MUST be
#    in westus: Foundry VNet injection is only supported there. Everything this
#    harness creates lives in ACCOUNT_LOCATION.
#  - westus is frequently out of VM capacity. The harness itself creates no VM,
#    but the gated deploy+invoke phase needs VNet line-of-sight to the account
#    private endpoint. If you stand up a jumpbox for that, put it in a
#    capacity-available region (CLIENT_LOCATION, default eastus) in its own VNet
#    and global-peer it to the westus VNet (+ link the private DNS zones to it).
ACCOUNT_LOCATION="${ACCOUNT_LOCATION:-westus}"
CLIENT_LOCATION="${CLIENT_LOCATION:-eastus}"

# Bound on concurrent what-if cells in phase 2 (independent, create nothing).
MAX_PARALLEL="${MAX_PARALLEL:-4}"

RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
PREFIX="${PREFIX:-azdnet${RUN_ID//-/}}"
PREFIX="${PREFIX:0:18}"  # keep within name limits

# BYO image, written into agent.yaml. Only pulled during the gated deploy phase
# (RUN_DEPLOY=true); the Foundry project MI is granted repository read on this
# RBAC+ABAC registry then. NOTE: this digest can be garbage-collected when the
# fixture image is rebuilt (a stale digest fails deploy with [ImageError]
# "Container image tag not found"). Refresh it, or use BUILD_IMAGE=true to build
# and push a fresh image from $ECHO_DUAL_DIR.
IMAGE="${IMAGE:-1756abcawemengncus3a16acr.azurecr.io/echodual@sha256:7d5009a3008258c242a1602dd1749926875b9810a7954c9b16d86ae5fecaff8a}"

# BUILD_IMAGE=true builds ~/agents/echo-dual into an ABAC-enabled ACR before any
# project fixture is generated, then rewrites IMAGE to the pushed tag.
BUILD_IMAGE="${BUILD_IMAGE:-false}"
ECHO_DUAL_DIR="${ECHO_DUAL_DIR:-$HOME/agents/echo-dual}"
IMAGE_REPO="${IMAGE_REPO:-network-e2e/echo-dual}"
IMAGE_TAG="${IMAGE_TAG:-$RUN_ID}"
ACR_SKU="${ACR_SKU:-Basic}"

# Phase 5 (deploy + invoke) uses the BYO-image deploy short-circuit. Off by
# default so phases 0-4 run against the current branch today.
RUN_DEPLOY="${RUN_DEPLOY:-false}"

# TARGET_RG lets investigation runs keep all test resources in a single RG.
# By default, keep the matrix-style split RGs for isolation/readability.
TARGET_RG="${TARGET_RG:-}"
VNET_RG="${VNET_RG:-${TARGET_RG:-${PREFIX}-vnet-rg}}"
DNS_RG="${DNS_RG:-${TARGET_RG:-${PREFIX}-dns-rg}}"        # external zones for the reference cells
VNET_NAME="${VNET_NAME:-${PREFIX}-vnet}"
# Dedicated VNet for the managed-iso cell (phase 3b). A dns=own account links
# its VNet to the AI privatelink zones, and a VNet may hold only one link per
# zone namespace; the phase-3 account already owns the shared VNet's links, so
# phase 3b needs its own VNet. Same address space is fine (the two VNets are
# never peered).
ISO_VNET_NAME="${ISO_VNET_NAME:-${PREFIX}-iso-vnet}"
VNET_CIDR="${VNET_CIDR:-192.168.0.0/16}"
DEFAULT_ACR_NAME="$(printf '%sacr' "$PREFIX" | tr -cd '[:alnum:]' | tr '[:upper:]' '[:lower:]')"
DEFAULT_ACR_NAME="${DEFAULT_ACR_NAME:0:50}"
ACR_RG="${ACR_RG:-${TARGET_RG:-$VNET_RG}}"
ACR_NAME="${ACR_NAME:-$DEFAULT_ACR_NAME}"

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
# args: <azure.yaml> <egress byo|managed> <subnet_mode create|reference> <dns_mode own|reference> [iso]
#   egress=byo     -> inject the agent into agentSubnet (BYO egress)
#   egress=managed -> omit agentSubnet (Microsoft-managed egress); [iso] sets isolationMode
# peSubnet is always written (required: the account data plane is never public).
write_network_block() {
  local file="$1" egress="$2" subnet_mode="$3" dns_mode="$4" iso="${5:-}"
  local agent pe
  if [[ "$subnet_mode" == "create" ]]; then
    agent="$AGENT_SUBNET_CREATE"; pe="$PE_SUBNET_CREATE"
  else
    agent="$AGENT_SUBNET_REF"; pe="$PE_SUBNET_REF"
  fi
  {
    if [[ "$egress" == "byo" ]]; then
      echo "agentSubnet:"
      echo "  vnet: \${AZURE_VNET_ID}"
      echo "  name: $agent"
      [[ "$subnet_mode" == "create" ]] && echo "  prefix: 192.168.10.0/24"
    elif [[ -n "$iso" ]]; then
      echo "isolationMode: $iso"
    fi
    echo "peSubnet:"
    echo "  vnet: \${AZURE_VNET_ID}"
    echo "  name: $pe"
    [[ "$subnet_mode" == "create" ]] && echo "  prefix: 192.168.11.0/24"
    if [[ "$dns_mode" == "reference" ]]; then
      echo "dns:"
      echo "  resourceGroup: $DNS_RG"
      echo "  subscription: \${AZURE_DNS_SUBSCRIPTION_ID}"
    fi
  } | inject_network_block "$file"
}

# write a hand-authored azure.yaml fixture for a matrix cell into a fresh
# project dir and create its azd environment. No `azd ai agent init --image`:
# phases 0-4 do not need the BYO-image init UX. The agent entry uses
# `image:` (so the synthesizer sets includeAcr=false, matching BYO image).
# args: <name> <egress byo|managed> <subnet_mode create|reference> <dns_mode own|reference> [iso]
setup_project() {
  local name="$1" egress="$2" subnet_mode="$3" dns_mode="$4" iso="${5:-}"
  PROJECT_DIR="$WORK_DIR/$name"
  rm -rf "${PROJECT_DIR:?}"; mkdir -p "$PROJECT_DIR"
  cat >"$PROJECT_DIR/azure.yaml" <<YAML
name: $AGENT_NAME
metadata:
  template: azure.ai.agents
infra:
  provider: microsoft.foundry
services:
  $AGENT_NAME:
    host: azure.ai.agent
    deployments: []
YAML
  # agent definition file required by the foundry provider (looked up at
  # <projectRoot>/<service project:>/agent.yaml; no project: => project root).
  # kind: hosted + image: => BYO pre-built image (no ACR build).
  cat >"$PROJECT_DIR/agent.yaml" <<YAML
kind: hosted
name: $AGENT_NAME
description: Hosted container agent (private-networking E2E)
image: $IMAGE
protocols:
  - protocol: responses
    version: 1.0.0
resources:
  cpu: "0.5"
  memory: 1Gi
YAML
  write_network_block "$PROJECT_DIR/azure.yaml" "$egress" "$subnet_mode" "$dns_mode" "$iso"
  ( cd "$PROJECT_DIR"
    run_capture "env-$name" azd env new "$name" \
      --subscription "$SUBSCRIPTION_ID" --location "$ACCOUNT_LOCATION"
    # The foundry provider requires the target RG name (the subscription-scoped
    # template creates it). Unique per project so cells don't collide.
    azd env set AZURE_RESOURCE_GROUP "${TARGET_RG:-${PREFIX}-${name}-rg}" >/dev/null
    azd env set AZURE_TENANT_ID "$(az account show --query tenantId -o tsv)" >/dev/null
    azd env set AZURE_VNET_ID "$VNET_ID" >/dev/null
    azd env set AZURE_DNS_SUBSCRIPTION_ID "$SUBSCRIPTION_ID" >/dev/null
    # BYO pre-built image: skip ACR build at provision AND deploy. Without this
    # the headless deploy defaults to "build" (no Dockerfile) and fails. Mirrors
    # what `azd ai agent init --image` persists.
    azd env set AZD_AGENT_SKIP_ACR true >/dev/null
  )
}

# --- phase 0: local gates ----------------------------------------------------

phase0_local_gates() {
  info "### phase 0: local gates (no Azure)"
  run_capture "00-azd-version" azd version
  run_capture "00-go-build" bash -c "cd '$SCRIPT_DIR/../../..' && go build ./..."
  # Refresh the dev extension from the CURRENT source so the run tests our code,
  # not a stale installed build. build (binary) -> pack -> publish (registers
  # capabilities incl. provisioning-provider + the microsoft.foundry provider)
  # -> install from the local source. Requires an up-to-date `azd x` tool.
  if [[ "${SKIP_EXT_REFRESH:-false}" != "true" ]]; then
    ( cd "$SCRIPT_DIR/../../.."
      azd extension uninstall azure.ai.agents >/dev/null 2>&1 || true
      run_capture "01-ext-build"   azd x build
      run_capture "01-ext-pack"    azd x pack
      run_capture "01-ext-publish" azd x publish
      run_capture "01-ext-install" azd extension install azure.ai.agents --source local
    )
  else
    warn "SKIP_EXT_REFRESH=true: using the already-installed azure.ai.agents extension"
  fi
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
    # idempotent: private-dns zone create errors if the zone already exists.
    if az network private-dns zone show -g "$DNS_RG" -n "$z" >/dev/null 2>&1; then
      info "dns zone $z already exists; reusing"
    else
      run_capture "12-zone-${z//./_}" az network private-dns zone create -g "$DNS_RG" -n "$z"
    fi
  done
}

# --- optional image build ----------------------------------------------------

build_byo_image() {
  if [[ "$BUILD_IMAGE" != "true" ]]; then
    return 0
  fi

  info "### image build: ABAC-enabled ACR + echo-dual"
  if [[ ! -f "$ECHO_DUAL_DIR/Dockerfile" ]]; then
    fatal "ECHO_DUAL_DIR does not contain a Dockerfile: $ECHO_DUAL_DIR"
  fi

  run_capture "13-rg-acr" az group create -n "$ACR_RG" -l "$ACCOUNT_LOCATION"
  if az acr show -n "$ACR_NAME" >/dev/null 2>&1; then
    local mode
    mode="$(az acr show -n "$ACR_NAME" --query roleAssignmentMode -o tsv 2>/dev/null || echo '')"
    if [[ "$mode" != *Abac* && "$mode" != *abac* ]]; then
      fatal "ACR $ACR_NAME exists but is not ABAC-enabled (roleAssignmentMode=$mode); choose a new ACR_NAME"
    fi
    info "ABAC-enabled ACR $ACR_NAME already exists; reusing"
  else
    run_capture "13-acr-create" az acr create -g "$ACR_RG" -n "$ACR_NAME" \
      --sku "$ACR_SKU" --location "$ACCOUNT_LOCATION" --role-assignment-mode rbac-abac
  fi

  local acr_id caller_id principal_type
  acr_id="$(az acr show -n "$ACR_NAME" --query id -o tsv)"
  # Avoid Microsoft Graph here: some tenants block `az ad signed-in-user show`
  # via Conditional Access. The ARM token contains the caller object id (`oid`).
  caller_id="$(az account get-access-token --resource https://management.azure.com/ \
    --query accessToken -o tsv | python3 -c 'import base64,json,sys; p=sys.stdin.read().strip().split(".")[1]; print(json.loads(base64.urlsafe_b64decode(p + "=" * (-len(p) % 4))).get("oid", ""))')"
  principal_type="$(az account show --query user.type -o tsv)"
  if [[ "$principal_type" == "servicePrincipal" ]]; then
    principal_type="ServicePrincipal"
  else
    principal_type="User"
  fi

  if [[ -n "$caller_id" ]]; then
    # ABAC-enabled registries require repository-scoped data-plane roles. The
    # caller queues the ACR Task and needs repository write to push the built
    # image. The project MI receives Repository Reader later for image pull.
    run_capture "13-acr-caller-writer" az role assignment create \
      --assignee-object-id "$caller_id" --assignee-principal-type "$principal_type" \
      --role "Container Registry Repository Writer" --scope "$acr_id" || \
      warn "caller repository-writer grant failed (may already exist)"
    sleep 30 # role propagation before the ACR Task push
  else
    warn "could not resolve caller object id; ensure caller has Container Registry Repository Writer"
  fi

  # ABAC-enabled repository permissions require the caller identity when ACR
  # Tasks authenticates to a source registry. Keep the literal [caller] quoted
  # so the shell does not interpret it as a glob.
  run_capture "13-acr-build" az acr build -r "$ACR_NAME" \
    -t "$IMAGE_REPO:$IMAGE_TAG" --source-acr-auth-id "[caller]" "$ECHO_DUAL_DIR"
  IMAGE="$ACR_NAME.azurecr.io/$IMAGE_REPO:$IMAGE_TAG"
  printf 'IMAGE=%s\nACR_NAME=%s\nACR_RG=%s\n' "$IMAGE" "$ACR_NAME" "$ACR_RG" >"$OUT_DIR/13-image.txt"
  info "IMAGE=$IMAGE"
}

# --- phase 2: what-if matrix -------------------------------------------------

# the matrix cells. The first BYO create/own cell is also the real-provision
# cell. Managed-egress cells omit agentSubnet; the last two exercise both
# isolationMode values (the managedNetworks child resource).
# fields: <egress> <subnet_mode> <dns_mode> [iso]
MATRIX=(
  "byo create own"
  "byo create reference"
  "byo reference own"
  "byo reference reference"
  "managed create own"
  "managed reference reference"
  "managed create own AllowInternetOutbound"
  "managed create own AllowOnlyApprovedOutbound"
)

phase2_whatif_matrix() {
  info "### phase 2: what-if matrix (no creation, up to ${MAX_PARALLEL} parallel)"
  local cell eg sm dm iso tag
  local -a pids=() tags=()
  local rc=0
  for cell in "${MATRIX[@]}"; do
    read -r eg sm dm iso <<<"$cell"
    tag="${eg}-${sm}-${dm}${iso:+-$iso}"
    # Throttle: wait for a slot when MAX_PARALLEL cells are in flight.
    while (( ${#pids[@]} >= MAX_PARALLEL )); do
      if ! wait "${pids[0]}"; then rc=1; warn "what-if[${tags[0]}] failed"; fi
      pids=("${pids[@]:1}"); tags=("${tags[@]:1}")
    done
    # Each cell uses an isolated project dir + azd env, and what-if creates
    # nothing against the shared VNet, so cells are safe to run concurrently.
    (
      setup_project "wi-$tag" "$eg" "$sm" "$dm" "$iso"
      cd "$PROJECT_DIR"
      preview_capture "20-whatif-$tag"
      info "ok: what-if[$tag] generated a valid plan"
    ) &
    pids+=("$!"); tags+=("$tag")
  done
  # Drain the rest.
  local i
  for i in "${!pids[@]}"; do
    if ! wait "${pids[$i]}"; then rc=1; warn "what-if[${tags[$i]}] failed"; fi
  done
  (( rc == 0 )) || die "phase 2: one or more what-if cells failed"
}

# --- phase 3: real provision (create/own) ------------------------------------

phase3_real_provision() {
  info "### phase 3: real provision (create+own)"
  setup_project "real" byo create own
  REAL_DIR="$PROJECT_DIR"
  ( cd "$REAL_DIR"
    STEP_TIMEOUT=1800 run_capture "30-provision" azd provision --no-prompt
    azd env get-values >"$OUT_DIR/30-env-after-provision.txt" 2>&1 || true
  )

  # resolve account for the live-topology assertions.
  RG="$(cd "$REAL_DIR" && azd env get-value AZURE_RESOURCE_GROUP)"
  ACCOUNT_NAME="$(cd "$REAL_DIR" && azd env get-value AZURE_AI_ACCOUNT_NAME)"

  # live topology assertions
  ( cd "$REAL_DIR"
    RG="$RG" ACCOUNT_NAME="$ACCOUNT_NAME" VNET_RG="$VNET_RG" VNET_NAME="$VNET_NAME" \
      AGENT_SUBNET="$AGENT_SUBNET_CREATE" PE_SUBNET="$PE_SUBNET_CREATE" \
      EXPECT_DNS_ZONES=own \
      bash "$SCRIPT_DIR/assert-resources.sh"
  ) 2>&1 | tee "$OUT_DIR/31-assert-resources.log"
}

# --- phase 3b: managed-egress isolationMode (gated) --------------------------

# Provisions the managed-egress AllowOnlyApprovedOutbound cell and asserts the
# managedNetworks/default child resource was accepted with that isolationMode.
# This is the one scenario `azd provision --preview` cannot confirm (the V2
# managed network is created, not just planned). Gated behind RUN_MANAGED_ISO
# because it provisions a second real account. Cleans up its own RG inline.
phase3b_managed_iso() {
  [[ "${RUN_MANAGED_ISO:-false}" == "true" ]] || { info "### phase 3b: managed-iso (skipped; set RUN_MANAGED_ISO=true)"; return 0; }
  info "### phase 3b: managed-egress AllowOnlyApprovedOutbound (real provision)"
  # Dedicated VNet (see ISO_VNET_NAME): a dns=own account links its VNet to the
  # AI privatelink zones, and a VNet may hold only one link per zone namespace.
  # The phase-3 account already linked the shared VNet, so the managed cell
  # provisions into its own VNet to create+link its zones without colliding.
  # (Brownfield/multi-account callers use dns: reference mode, which skips the
  # link entirely.)
  run_capture "31-iso-vnet" az network vnet create -g "$VNET_RG" -n "$ISO_VNET_NAME" \
    --address-prefixes "$VNET_CIDR" -l "$ACCOUNT_LOCATION"
  local iso_vnet_id
  iso_vnet_id="$(az network vnet show -g "$VNET_RG" -n "$ISO_VNET_NAME" --query id -o tsv)"
  setup_project "iso" managed create own AllowOnlyApprovedOutbound
  local iso_dir="$PROJECT_DIR" iso_rg iso_acct iso_mode
  ( cd "$iso_dir"
    azd env set AZURE_VNET_ID "$iso_vnet_id" >/dev/null   # override the shared VNet
    STEP_TIMEOUT=1800 run_capture "32-provision-iso" azd provision --no-prompt
  )
  iso_rg="$(cd "$iso_dir" && azd env get-value AZURE_RESOURCE_GROUP)"
  iso_acct="$(cd "$iso_dir" && azd env get-value AZURE_AI_ACCOUNT_NAME)"
  iso_mode="$(az resource show \
    --ids "/subscriptions/$SUBSCRIPTION_ID/resourceGroups/$iso_rg/providers/Microsoft.CognitiveServices/accounts/$iso_acct/managedNetworks/default" \
    --api-version 2025-10-01-preview --query 'properties.managedNetwork.isolationMode' -o tsv 2>/dev/null || echo '')"
  # Always clean up the second account RG, then fail if the assertion missed.
  run_capture "33-del-iso-rg" az group delete -n "$iso_rg" --yes --no-wait || true
  if [[ "$iso_mode" == "AllowOnlyApprovedOutbound" ]]; then
    info "ok: managedNetworks/default isolationMode=$iso_mode"
  else
    die "managedNetworks/default isolationMode mismatch: got '$iso_mode', want AllowOnlyApprovedOutbound"
  fi
}

# grant the Foundry project managed identity repository read on the BYO
# registry. This ACR uses RBAC+ABAC, so the correct role is the ABAC-aware
# "Container Registry Repository Reader" (not the legacy AcrPull). Only needed
# for the gated deploy phase (image pull).
grant_acr_pull() {
  local acr_login acr_name acr_id project_id pid
  acr_login="${IMAGE%%/*}"
  acr_name="${acr_login%%.*}"
  acr_id="$(az acr show -n "$acr_name" --query id -o tsv 2>/dev/null || echo '')"
  if [[ -z "$acr_id" ]]; then
    warn "could not resolve ACR '$acr_name' id; grant the project MI 'Container Registry Repository Reader' manually"
    return 0
  fi

  project_id="$(cd "$REAL_DIR" && azd env get-value AZURE_AI_PROJECT_ID 2>/dev/null || echo '')"
  if [[ -n "$project_id" ]]; then
    pid="$(az rest --method get \
      --url "https://management.azure.com${project_id}?api-version=2025-04-01-preview" \
      --query identity.principalId -o tsv 2>/dev/null || echo '')"
  fi
  # Fallback for older RP/API shapes, but the hosted-agent image pull uses the
  # project MI when a project-scoped identity exists.
  if [[ -z "${pid:-}" || "$pid" == "null" ]]; then
    pid="$(az cognitiveservices account show -g "$RG" -n "$ACCOUNT_NAME" \
      --query identity.principalId -o tsv 2>/dev/null || echo '')"
  fi
  if [[ -z "${pid:-}" || "$pid" == "null" ]]; then
    warn "could not resolve project MI principalId; grant repository read manually"
    return 0
  fi
  run_capture "30-acr-pull" az role assignment create --assignee-object-id "$pid" \
    --assignee-principal-type ServicePrincipal --role "Container Registry Repository Reader" \
    --scope "$acr_id" || \
    warn "repository-read grant failed (may already exist or need an ABAC condition)"
}

# --- phase 4: eject idempotency (Scenario 2) ---------------------------------

phase4_eject() {
  info "### phase 4: eject idempotency on the provisioned env"
  ( cd "$REAL_DIR"
    run_capture "40-eject" azd ai agent init --infra
    # the ejected params must preserve the ${VAR} token (regression guard).
    assert_contains "$(cat infra/main.parameters.json)" '${AZURE_VNET_ID}' \
      'ejected params preserve vnet ${VAR} placeholder'
    # what-if against the live account: ejected on-disk template + provision-time
    # ${VAR} resolution must reproduce the same topology -> no changes.
    preview_capture "41-eject-whatif"
    if grep -qiE 'no changes|nothing to (deploy|change)' "$OUT_DIR/41-eject-whatif.log"; then
      info "ok: eject what-if reports no changes (idempotent)"
    else
      warn "eject what-if shows changes; inspect $OUT_DIR/41-eject-whatif.log"
    fi
  )
}

# --- phase 5: deploy + invoke (gated: RUN_DEPLOY=true) -----------------------

phase5_deploy_invoke() {
  if [[ "$RUN_DEPLOY" != "true" ]]; then
    warn "RUN_DEPLOY!=true: skipping deploy+invoke."
    warn "deploy+invoke also needs VNet line-of-sight to the westus account PE; if using a jumpbox, put it in ${CLIENT_LOCATION} (peered to the westus VNet)."
    return 0
  fi
  info "### phase 5: deploy + invoke under the VNet"
  jumpbox_up          # line-of-sight VM (in-VNet, or peered fallback)
  jumpbox_socks_open  # local SOCKS5 proxy into the VNet
  grant_acr_pull      # repository read for the BYO image pull
  # Tunnel azd's data-plane HTTPS to the private account FQDNs through the
  # jumpbox. SOCKS5 (socks5h) does remote DNS on the jumpbox, which resolves
  # the privatelink names to the PE IP. azd itself runs here (our extension).
  ( cd "$REAL_DIR"
    export HTTPS_PROXY="socks5://127.0.0.1:${JB_SOCKS_PORT}"
    export HTTP_PROXY="$HTTPS_PROXY" ALL_PROXY="$HTTPS_PROXY" NO_PROXY="127.0.0.1,localhost"
    STEP_TIMEOUT=1800 run_capture "50-deploy" azd deploy --no-prompt
    azd ai agent show --output json >"$OUT_DIR/51-show.json" 2>&1 || true
    STEP_TIMEOUT=300 run_capture "52-invoke" azd ai agent invoke --new-session "hello, are you up?"
  )
}

# --- phase 6: teardown -------------------------------------------------------

phase6_teardown() {
  if [[ "$KEEP" == "true" ]]; then warn "KEEP=true: skipping teardown"; return 0; fi
  info "### phase 6: teardown"
  jumpbox_down
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
    echo "client_location=$CLIENT_LOCATION max_parallel=$MAX_PARALLEL"
    echo "image=$IMAGE"
    echo "build_image=$BUILD_IMAGE"
    echo "echo_dual_dir=$ECHO_DUAL_DIR"
    echo "acr_name=$ACR_NAME acr_rg=$ACR_RG"
    echo "target_rg=$TARGET_RG"
    echo "run_deploy=$RUN_DEPLOY"
    echo "work_dir=$WORK_DIR"
    echo "out_dir=$OUT_DIR"
    echo "vnet_rg=$VNET_RG dns_rg=$DNS_RG vnet=$VNET_NAME"
    azd version
  } >"$OUT_DIR/00-context.txt"

  trap 'phase6_teardown' EXIT
  # MAX_PHASE lets you stop early while iterating (e.g. MAX_PHASE=2 for the cheap
  # VNet + what-if gates). Teardown still runs via the EXIT trap unless KEEP=true.
  local max="${MAX_PHASE:-6}"
  phase0_local_gates
  if (( max >= 1 )); then phase1_shared_infra; fi
  build_byo_image
  if (( max >= 2 )); then phase2_whatif_matrix; fi
  if (( max >= 3 )); then phase3_real_provision; fi
  if (( max >= 3 )); then phase3b_managed_iso; fi
  if (( max >= 4 )); then phase4_eject; fi
  if (( max >= 5 )); then phase5_deploy_invoke; fi
  # teardown runs via trap
  info "E2E complete (through phase $max). Logs: $OUT_DIR"
}

# Run main only when executed directly; sourcing (e.g. a phase-5 iteration
# driver) gets the functions/vars without kicking off a full run.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  main "$@"
fi
