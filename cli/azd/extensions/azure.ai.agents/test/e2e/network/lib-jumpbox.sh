# Jumpbox helpers for phase 5 (deploy + invoke) line-of-sight.
#
# The account data plane is private (publicNetworkAccess=Disabled), so a public
# host cannot reach the account FQDNs. Instead of running azd on a VM (which
# would mean replicating our extension build + managed-identity auth there),
# we stand up a jumpbox with line-of-sight and expose it as a local SOCKS5
# proxy. azd deploy/invoke then run on THIS host -- using the extension we
# built from the current branch and the existing azd env -- while data-plane
# HTTPS to the private FQDNs is tunneled through the jumpbox (SOCKS5 remote DNS,
# so the jumpbox resolves the privatelink names to the PE IP).
#
# Reachability is captured two ways:
#   - Preferred: VM inside the foundry VNet (ACCOUNT_LOCATION). The dns=own
#     privatelink zones are already linked to that VNet and routing is
#     intra-VNet, so remote DNS + routing work with no extra wiring.
#   - Fallback (ACCOUNT_LOCATION has no VM capacity): VM in a peered VNet in one
#     of JB_FALLBACK_LOCATIONS. We global-peer it to the foundry VNet and pin
#     the account FQDNs to the PE IP in the VM's /etc/hosts (the peered VNet is
#     not linked to the private DNS zones, so resolution is done via hosts).
#
# VM capacity is frequently restricted per region+size, so VM creation loops
# over JB_VM_SIZES (and, in the fallback, over JB_FALLBACK_LOCATIONS) until an
# allocation succeeds. A NIC is created once per region and reused across size
# attempts so failed allocations do not orphan public IPs/NICs.
#
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

# shellcheck shell=bash

# State (set during the run).
JB_LOCATION=""   # resolved region (ACCOUNT_LOCATION or a fallback)
JB_HOST=""       # public IP
JB_SSH_KEY=""    # private key path
JB_SSH_PID=""    # SOCKS tunnel pid
JB_RG=""         # resource group holding the VM
JB_VNET=""       # vnet holding the VM (foundry vnet, or a fallback vnet)

# jumpbox_init : resolve config defaults that depend on run-time vars (PREFIX,
# CLIENT_LOCATION). Called at the start of jumpbox_up so sourcing this file does
# not require those vars to exist yet (set -u safe).
jumpbox_init() {
  JB_VM_NAME="${JB_VM_NAME:-${PREFIX}-jb}"
  # Capacity-restricted SKUs are common; try a spread until one allocates.
  JB_VM_SIZES="${JB_VM_SIZES:-Standard_D2as_v5 Standard_D2s_v5 Standard_B2s Standard_B2ms Standard_DS1_v2 Standard_A2_v2}"
  JB_SUBNET_NAME="${JB_SUBNET_NAME:-jumpbox-subnet}"
  JB_SUBNET_CIDR="${JB_SUBNET_CIDR:-192.168.50.0/24}"   # inside the foundry VNet
  JB_FALLBACK_LOCATIONS="${JB_FALLBACK_LOCATIONS:-$CLIENT_LOCATION eastus2 westus2 westus3 centralus}"
  JB_FALLBACK_VNET_CIDR="${JB_FALLBACK_VNET_CIDR:-10.60.0.0/16}"
  JB_FALLBACK_SUBNET_CIDR="${JB_FALLBACK_SUBNET_CIDR:-10.60.0.0/24}"
  JB_SOCKS_PORT="${JB_SOCKS_PORT:-1080}"
  JB_ADMIN="${JB_ADMIN:-azureuser}"
}

# jumpbox_ensure_net <rg> <loc> <vnet> <vnet_cidr> <subnet> <subnet_cidr> <create_vnet>
# Idempotently ensure a regional NSG (SSH open initially -- this host's
# Azure-facing NAT IP is not reliably known up front and may differ from a
# public echo service; jumpbox_narrow_nsg tightens it once SSH is up), and the
# jumpbox subnet (associated with the NSG). Sets JB_NSG.
jumpbox_ensure_net() {
  local rg="$1" loc="$2" vnet="$3" vnet_cidr="$4" subnet="$5" subnet_cidr="$6" create_vnet="$7"
  JB_NSG="${JB_VM_NAME}-${loc}-nsg"
  if ! az network nsg show -g "$rg" -n "$JB_NSG" >/dev/null 2>&1; then
    run_retry 3 "jb-nsg-$loc" az network nsg create -g "$rg" -n "$JB_NSG" -l "$loc"
    run_retry 3 "jb-nsg-ssh-$loc" az network nsg rule create -g "$rg" --nsg-name "$JB_NSG" \
      -n allow-ssh --priority 1000 --access Allow --protocol Tcp --direction Inbound \
      --destination-port-ranges 22 --source-address-prefixes Internet
  fi
  if [[ "$create_vnet" == true ]] && ! az network vnet show -g "$rg" -n "$vnet" >/dev/null 2>&1; then
    run_retry 3 "jb-vnet-$loc" az network vnet create -g "$rg" -n "$vnet" \
      --address-prefixes "$vnet_cidr" -l "$loc"
  fi
  if ! az network vnet subnet show -g "$rg" --vnet-name "$vnet" -n "$subnet" >/dev/null 2>&1; then
    run_retry 3 "jb-subnet-$loc" az network vnet subnet create -g "$rg" --vnet-name "$vnet" \
      -n "$subnet" --address-prefixes "$subnet_cidr" --network-security-group "$JB_NSG"
  fi
}

# jumpbox_vm_sizeloop <rg> <loc> <vnet> <subnet> : create a NIC (reused across
# attempts) and try JB_VM_SIZES until one allocates. Returns 0 on success.
jumpbox_vm_sizeloop() {
  local rg="$1" loc="$2" vnet="$3" subnet="$4"
  local pip="${JB_VM_NAME}-${loc}-pip" nic="${JB_VM_NAME}-${loc}-nic" size
  az network public-ip show -g "$rg" -n "$pip" >/dev/null 2>&1 || \
    run_retry 3 "jb-pip-$loc" az network public-ip create -g "$rg" -n "$pip" --sku Standard -l "$loc"
  az network nic show -g "$rg" -n "$nic" >/dev/null 2>&1 || \
    run_retry 3 "jb-nic-$loc" az network nic create -g "$rg" -n "$nic" -l "$loc" \
      --vnet-name "$vnet" --subnet "$subnet" --public-ip-address "$pip"
  for size in $JB_VM_SIZES; do
    if STEP_TIMEOUT=600 run_capture "jb-vm-${loc}-${size}" az vm create -g "$rg" -n "$JB_VM_NAME" \
         --image Ubuntu2204 --size "$size" --location "$loc" --nics "$nic" \
         --admin-username "$JB_ADMIN" --ssh-key-values "${JB_SSH_KEY}.pub"; then
      JB_VM_SIZE="$size"; info "jumpbox: VM created in $loc as $size"; return 0
    fi
    warn "jumpbox: $size unavailable in $loc; trying next size"
    az vm delete -g "$rg" -n "$JB_VM_NAME" --yes 2>/dev/null || true  # clear any partial VM
  done
  return 1
}

# jumpbox_peer_and_pin <foundry_vnet_id> <jb_vnet_id> : global-peer the fallback
# VNet to the foundry VNet, then pin the account FQDNs to the PE IP in the VM's
# /etc/hosts (the peered VNet is not linked to the private DNS zones).
jumpbox_peer_and_pin() {
  local fvnet="$1" jbvnet="$2"
  run_capture "jb-peer-out" az network vnet peering create -g "$VNET_RG" \
    --vnet-name "$VNET_NAME" -n "to-jb" --remote-vnet "$jbvnet" \
    --allow-vnet-access --allow-forwarded-traffic
  run_capture "jb-peer-in" az network vnet peering create -g "$JB_RG" \
    --vnet-name "$JB_VNET" -n "to-foundry" --remote-vnet "$fvnet" \
    --allow-vnet-access --allow-forwarded-traffic

  # Resolve the account FQDNs from private DNS records and pin each FQDN to its
  # matching PE IP. A private endpoint can allocate different IPs for
  # services.ai, openai, and cognitiveservices; pinning all FQDNs to the first NIC
  # IP causes "Traffic is not from an approved private endpoint" in dns=reference
  # fallback runs.
  local sub hosts_lines zone rg ip fqdn
  sub="$(az cognitiveservices account show -g "$RG" -n "$ACCOUNT_NAME" \
    --query "properties.customSubDomainName" -o tsv 2>/dev/null)"
  [[ -z "$sub" ]] && sub="$ACCOUNT_NAME"
  hosts_lines=""
  for zone in privatelink.services.ai.azure.com privatelink.openai.azure.com \
              privatelink.cognitiveservices.azure.com; do
    rg="$RG"
    if [[ -n "${DNS_RG:-}" ]] && \
       az network private-dns record-set a show -g "$DNS_RG" -z "$zone" -n "$sub" >/dev/null 2>&1; then
      rg="$DNS_RG"
    fi
    ip="$(az network private-dns record-set a show -g "$rg" -z "$zone" -n "$sub" \
      --query "aRecords[0].ipv4Address" -o tsv 2>/dev/null || echo '')"
    [[ -n "$ip" ]] || { warn "could not resolve private DNS A record for $sub in $zone"; continue; }
    fqdn="${sub}.${zone#privatelink.}"
    hosts_lines+="$ip $fqdn"$'\n'
  done
  [[ -n "$hosts_lines" ]] || { warn "could not resolve PE DNS records; /etc/hosts pin skipped"; return 0; }
  info "jumpbox: pinning account FQDNs via private DNS records: ${hosts_lines//$'\n'/; }"
  jumpbox_ssh "cat <<'EOF' | sudo tee -a /etc/hosts >/dev/null
$hosts_lines
EOF"
}

# jumpbox_ssh <remote-cmd> : run a command on the jumpbox (capped).
jumpbox_ssh() {
  timeout 60 ssh -i "$JB_SSH_KEY" -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 \
    "${JB_ADMIN}@${JB_HOST}" "$@"
}

# jumpbox_wait_ssh : block until SSH answers (capped ~3 min).
jumpbox_wait_ssh() {
  local i
  for i in $(seq 1 18); do
    if jumpbox_ssh true 2>/dev/null; then info "jumpbox SSH reachable"; return 0; fi
    sleep 10
  done
  die "jumpbox SSH not reachable at $JB_HOST after ~3m"
}

# jumpbox_narrow_nsg : once SSH works, tighten the allow-ssh rule from Internet
# to the /24 of the client IP the jumpbox actually sees (the Azure-facing NAT
# may differ from a public echo IP, and the pool can rotate within a /24).
jumpbox_narrow_nsg() {
  local realip cidr
  realip="$(jumpbox_ssh 'echo $SSH_CONNECTION' 2>/dev/null | awk '{print $1}')"
  [[ "$realip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] \
    || { warn "jumpbox: could not detect client IP; SSH left open to Internet"; return 0; }
  cidr="$(awk -F. '{print $1"."$2"."$3".0/24"}' <<<"$realip")"
  run_capture "jb-nsg-narrow" az network nsg rule update -g "$JB_RG" --nsg-name "$JB_NSG" \
    -n allow-ssh --source-address-prefixes "$cidr" || true
  info "jumpbox: narrowed SSH to $cidr (client $realip)"
}

# jumpbox_up : stand up the jumpbox with line-of-sight. Sets JB_HOST.
jumpbox_up() {
  info "### jumpbox: provisioning line-of-sight VM"
  jumpbox_init
  JB_SSH_KEY="$WORK_DIR/jb_id_ed25519"
  [[ -f "$JB_SSH_KEY" ]] || ssh-keygen -t ed25519 -N "" -f "$JB_SSH_KEY" -q

  # Preferred: VM inside the foundry VNet (line-of-sight is structural).
  jumpbox_ensure_net "$VNET_RG" "$ACCOUNT_LOCATION" "$VNET_NAME" "" \
    "$JB_SUBNET_NAME" "$JB_SUBNET_CIDR" false
  if jumpbox_vm_sizeloop "$VNET_RG" "$ACCOUNT_LOCATION" "$VNET_NAME" "$JB_SUBNET_NAME"; then
    JB_LOCATION="$ACCOUNT_LOCATION"; JB_RG="$VNET_RG"; JB_VNET="$VNET_NAME"
  else
    warn "jumpbox: no VM capacity in $ACCOUNT_LOCATION; trying peered fallback regions: $JB_FALLBACK_LOCATIONS"
    JB_RG="${PREFIX}-jb-rg"
    local rgok=false loc jbvnet
    for loc in $JB_FALLBACK_LOCATIONS; do
      [[ "$loc" == "$ACCOUNT_LOCATION" ]] && continue
      jbvnet="${PREFIX}-jb-${loc}-vnet"
      [[ "$rgok" == false ]] && { run_capture "jb-rg" az group create -n "$JB_RG" -l "$loc"; rgok=true; }
      jumpbox_ensure_net "$JB_RG" "$loc" "$jbvnet" "$JB_FALLBACK_VNET_CIDR" \
        "$JB_SUBNET_NAME" "$JB_FALLBACK_SUBNET_CIDR" true
      if jumpbox_vm_sizeloop "$JB_RG" "$loc" "$jbvnet" "$JB_SUBNET_NAME"; then
        JB_LOCATION="$loc"; JB_VNET="$jbvnet"
        JB_HOST="$(az vm show -d -g "$JB_RG" -n "$JB_VM_NAME" --query publicIps -o tsv)"
        jumpbox_wait_ssh
        jumpbox_peer_and_pin "$VNET_ID" \
          "$(az network vnet show -g "$JB_RG" -n "$jbvnet" --query id -o tsv)"
        break
      fi
      warn "jumpbox: no VM capacity in $loc"
    done
    [[ -n "${JB_LOCATION:-}" && "$JB_LOCATION" != "$ACCOUNT_LOCATION" ]] \
      || die "jumpbox: no VM capacity in $ACCOUNT_LOCATION or fallback regions ($JB_FALLBACK_LOCATIONS)"
  fi

  JB_HOST="$(az vm show -d -g "$JB_RG" -n "$JB_VM_NAME" --query publicIps -o tsv)"
  info "jumpbox: $JB_VM_NAME up in $JB_LOCATION at $JB_HOST"
  jumpbox_wait_ssh
  jumpbox_narrow_nsg
}

# jumpbox_socks_open : open a background SSH SOCKS5 tunnel on localhost.
jumpbox_socks_open() {
  jumpbox_socks_close
  timeout 30 ssh -i "$JB_SSH_KEY" -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 -o ExitOnForwardFailure=yes \
    -fN -D "127.0.0.1:${JB_SOCKS_PORT}" "${JB_ADMIN}@${JB_HOST}"
  JB_SSH_PID="$(pgrep -f "ssh.*-D 127.0.0.1:${JB_SOCKS_PORT}.*${JB_HOST}" | head -1)"
  [[ -n "$JB_SSH_PID" ]] || die "jumpbox: SOCKS tunnel failed to start"
  info "jumpbox: SOCKS5 proxy on 127.0.0.1:${JB_SOCKS_PORT} (pid $JB_SSH_PID)"
}

jumpbox_socks_close() {
  local port="${JB_SOCKS_PORT:-1080}"
  [[ -n "$JB_SSH_PID" ]] && kill "$JB_SSH_PID" 2>/dev/null || true
  pkill -f "ssh.*-D 127.0.0.1:${port}.*${JB_HOST:-_none_}" 2>/dev/null || true
  JB_SSH_PID=""
}

# jumpbox_down : close the tunnel and delete the fallback RG (the in-VNet VM is
# removed with VNET_RG at teardown).
jumpbox_down() {
  jumpbox_socks_close
  if [[ -n "${JB_RG:-}" && "${JB_RG:-}" != "${VNET_RG:-}" ]]; then
    run_capture "jb-del-rg" az group delete -n "$JB_RG" --yes --no-wait || true
  fi
}
