#!/usr/bin/env bash
# run-network-e2e.sh : end-to-end validation of Foundry private networking for
# `host: microsoft.foundry`, optimized for minimal Azure resource-operation time.
#
# Strategy (see README.md for the cost rationale):
#   - ONE real network account is provisioned (the create+own matrix cell).
#   - The other matrix cells and the bicep-less vs eject code paths are verified
#     with `azd provision --preview` (ARM what-if) which creates nothing.
#   - A shared BYO VNet (+ optional pre-created subnets / DNS zones) is created
#     once and reused across cells.
#
# Phases 0-4 validate all the *networking* code and do NOT require the BYO-image
# init UX (`azd ai agent init --image`, PR 8689). The project is hand-authored
# (azure.yaml fixture), so it runs against the current branch today. Phase 5
# (deploy + invoke the BYO image under the VNet) needs the deploy-time pre-built
# short-circuit from PR 8689 and is gated behind RUN_DEPLOY=true.
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

# --- configuration (override via env) ----------------------------------------

# Hard requirement from the test plan: the network-enabled Foundry account must
# be in westus. VM-style quota is not consumed by this template, but override if
# a region hits capacity.
ACCOUNT_LOCATION="${ACCOUNT_LOCATION:-westus}"

RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
PREFIX="${PREFIX:-azdnet${RUN_ID//-/}}"
PREFIX="${PREFIX:0:18}"  # keep within name limits

# BYO image, written into agent.yaml. Only pulled during the gated deploy phase
# (RUN_DEPLOY=true); the Foundry project MI is granted repository read on this
# RBAC+ABAC registry then.
IMAGE="${IMAGE:-1756abcawemengncus3a16acr.azurecr.io/echodual@sha256:76a9463463acf11d4068e8468fb232a3de0709177b6b35de95de6a34b33fa686}"

# BUILD_IMAGE=true builds ~/agents/echo-dual into an ABAC-enabled ACR before any
# project fixture is generated, then rewrites IMAGE to the pushed tag.
BUILD_IMAGE="${BUILD_IMAGE:-false}"
ECHO_DUAL_DIR="${ECHO_DUAL_DIR:-$HOME/agents/echo-dual}"
IMAGE_REPO="${IMAGE_REPO:-network-e2e/echo-dual}"
IMAGE_TAG="${IMAGE_TAG:-$RUN_ID}"
ACR_SKU="${ACR_SKU:-Basic}"

# Phase 5 (deploy + invoke) needs the BYO-image deploy short-circuit from PR
# 8689. Off by default so phases 0-4 run against the current branch today.
RUN_DEPLOY="${RUN_DEPLOY:-false}"

# TARGET_RG lets investigation runs keep all test resources in a single RG.
# By default, keep the matrix-style split RGs for isolation/readability.
TARGET_RG="${TARGET_RG:-}"
VNET_RG="${VNET_RG:-${TARGET_RG:-${PREFIX}-vnet-rg}}"
DNS_RG="${DNS_RG:-${TARGET_RG:-${PREFIX}-dns-rg}}"        # external zones for the reference cells
VNET_NAME="${VNET_NAME:-${PREFIX}-vnet}"
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

# write a hand-authored azure.yaml fixture for a matrix cell into a fresh
# project dir and create its azd environment. No `azd ai agent init --image`:
# phases 0-4 do not need the BYO-image init UX (PR 8689). The agent entry uses
# `image:` (so the synthesizer sets includeAcr=false, matching BYO image).
# args: <name> <subnet_mode create|reference> <dns_mode own|reference>
setup_project() {
  local name="$1" subnet_mode="$2" dns_mode="$3"
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
  write_network_block "$PROJECT_DIR/azure.yaml" "$subnet_mode" "$dns_mode"
  ( cd "$PROJECT_DIR"
    run_capture "env-$name" azd env new "$name" \
      --subscription "$SUBSCRIPTION_ID" --location "$ACCOUNT_LOCATION"
    # The foundry provider requires the target RG name (the subscription-scoped
    # template creates it). Unique per project so cells don't collide.
    azd env set AZURE_RESOURCE_GROUP "${TARGET_RG:-${PREFIX}-${name}-rg}" >/dev/null
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
    setup_project "wi-$tag" "$sm" "$dm"
    ( cd "$PROJECT_DIR"
      # A successful subscription-scoped ARM what-if is the gate: it proves the
      # synthesized template is valid AND that ARM accepts it against the real
      # VNet. For reference cells this also validates that the pre-created
      # subnets/zones exist and the agent-subnet delegation is correct (ARM
      # what-if fails otherwise). Creates nothing. preview_capture returns
      # non-zero on failure, which aborts the run under set -e.
      preview_capture "20-whatif-$tag"
      info "ok: what-if[$tag] generated a valid plan"
    )
  done
}

# --- phase 3: real provision (create/own) ------------------------------------

phase3_real_provision() {
  info "### phase 3: real provision (create+own)"
  setup_project "real" create own
  REAL_DIR="$PROJECT_DIR"
  ( cd "$REAL_DIR"
    run_capture "30-provision" azd provision --no-prompt
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

# grant the Foundry project managed identity repository read on the BYO
# registry. This ACR uses RBAC+ABAC, so the correct role is the ABAC-aware
# "Container Registry Repository Reader" (not the legacy AcrPull). Only needed
# for the gated deploy phase (image pull).
grant_acr_pull() {
  local acr_login acr_name acr_id pid
  acr_login="${IMAGE%%/*}"
  acr_name="${acr_login%%.*}"
  acr_id="$(az acr show -n "$acr_name" --query id -o tsv 2>/dev/null || echo '')"
  if [[ -z "$acr_id" ]]; then
    warn "could not resolve ACR '$acr_name' id; grant the project MI 'Container Registry Repository Reader' manually"
    return 0
  fi
  pid="$(az cognitiveservices account show -g "$RG" -n "$ACCOUNT_NAME" \
    --query identity.principalId -o tsv 2>/dev/null || echo '')"
  if [[ -z "$pid" || "$pid" == "null" ]]; then
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

# --- phase 5: deploy + invoke (gated: needs PR 8689) -------------------------

phase5_deploy_invoke() {
  if [[ "$RUN_DEPLOY" != "true" ]]; then
    warn "RUN_DEPLOY!=true: skipping deploy+invoke (needs the BYO-image deploy short-circuit from PR 8689)"
    return 0
  fi
  info "### phase 5: deploy + invoke under the VNet"
  grant_acr_pull   # repository read for the BYO image pull
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
  if (( max >= 4 )); then phase4_eject; fi
  if (( max >= 5 )); then phase5_deploy_invoke; fi
  # teardown runs via trap
  info "E2E complete (through phase $max). Logs: $OUT_DIR"
}

main "$@"
