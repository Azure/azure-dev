#!/usr/bin/env bash
# assert-resources.sh : verify the live topology of a provisioned
# network-secured Foundry account matches the spec. Sourced or run with the
# provisioned azd env active (cwd = project dir) and these vars exported:
#   RG               resource group of the Foundry account
#   ACCOUNT_NAME     Cognitive Services account name
#   VNET_RG          resource group holding the BYO vnet
#   VNET_NAME        BYO vnet name
#   AGENT_SUBNET     agent subnet name
#   PE_SUBNET        private-endpoint subnet name
#   EXPECT_DNS_ZONES "own" | "reference"
#
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "$SCRIPT_DIR/lib.sh"

assert_account_private() {
  local j
  j="$(az cognitiveservices account show -g "$RG" -n "$ACCOUNT_NAME" -o json)"
  assert_eq "$(jq -r '.properties.publicNetworkAccess' <<<"$j")" "Disabled" \
    "account publicNetworkAccess"
  assert_eq "$(jq -r '.properties.networkAcls.defaultAction' <<<"$j")" "Deny" \
    "account networkAcls.defaultAction"
  # bypass should allow Azure trusted services
  assert_contains "$(jq -r '.properties.networkAcls.bypass // ""' <<<"$j")" \
    "AzureServices" "account networkAcls.bypass"
}

assert_private_endpoint() {
  local pes count peid groups
  pes="$(az network private-endpoint list -g "$RG" -o json)"
  count="$(jq '[.[] | select(.privateLinkServiceConnections[]?.privateLinkServiceId
    | ascii_downcase | contains("/accounts/" + ($acct|ascii_downcase)))]
    | length' --arg acct "$ACCOUNT_NAME" <<<"$pes")"
  assert_ge "${count:-0}" 1 "account private endpoint count"
  peid="$(jq -r '.[0].id' <<<"$pes")"
  groups="$(az network private-endpoint show --ids "$peid" \
    --query 'privateLinkServiceConnections[0].groupIds' -o tsv 2>/dev/null || echo '')"
  assert_contains "$groups" "account" "private endpoint groupIds"
}

assert_subnet_delegation() {
  local del
  del="$(az network vnet subnet show -g "$VNET_RG" --vnet-name "$VNET_NAME" \
    -n "$AGENT_SUBNET" --query 'delegations[].serviceName' -o tsv 2>/dev/null || echo '')"
  assert_contains "$del" "Microsoft.App/environments" "agent subnet delegation"
}

assert_dns_zones() {
  if [[ "${EXPECT_DNS_ZONES:-own}" == "own" ]]; then
    local zones
    zones="$(az network private-dns zone list -g "$RG" --query '[].name' -o tsv 2>/dev/null || echo '')"
    assert_contains "$zones" "privatelink.services.ai.azure.com" "ai services dns zone"
    assert_contains "$zones" "privatelink.openai.azure.com"       "openai dns zone"
    assert_contains "$zones" "privatelink.cognitiveservices.azure.com" "cognitive dns zone"
  else
    info "EXPECT_DNS_ZONES=reference: zones live in external RG; skipping in-RG check"
  fi
}

# Real BYO-egress signal read off the account resource itself (not azd's own
# echoed AZURE_FOUNDRY_NETWORK_MODE output): the account's agent network
# injection must reference the customer agent subnet, with the Microsoft-managed
# network disabled. The output-variable classification is covered separately by
# the synthesizer unit tests (wantMode cases).
assert_byo_network_injection() {
  local j inj
  j="$(az cognitiveservices account show -g "$RG" -n "$ACCOUNT_NAME" -o json)"
  inj="$(jq -c '.properties.networkInjections[]? | select(.scenario=="agent")' <<<"$j")"
  assert_contains "$(jq -r '.subnetArmId // ""' <<<"$inj")" \
    "/subnets/$AGENT_SUBNET" "account agent networkInjection subnet"
  assert_eq "$(jq -r '.useMicrosoftManagedNetwork' <<<"$inj")" "false" \
    "account agent networkInjection useMicrosoftManagedNetwork"
}

main() {
  : "${RG:?}" "${ACCOUNT_NAME:?}" "${VNET_RG:?}" "${VNET_NAME:?}" "${AGENT_SUBNET:?}"
  info "asserting live topology for account=$ACCOUNT_NAME rg=$RG"
  assert_account_private
  assert_private_endpoint
  assert_subnet_delegation
  assert_dns_zones
  assert_byo_network_injection
  info "ALL RESOURCE ASSERTIONS PASSED"
}

# only run main when executed directly (not when sourced)
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then main "$@"; fi
