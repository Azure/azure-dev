// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v7"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

func resourceCheck(id, scope, expected string) Check {
	return Check{
		ID: id, Scope: scope, Status: Unverified, Mandatory: true, Expected: expected,
		Observed: "required ARM evidence is unavailable", Missing: "readable, explicit resource properties",
		Remediation: "provide the stated evidence or ask the resource administrator to correct the configuration",
	}
}

func resourceResult(c Check, status Status, observed string) Check {
	c.Status, c.Observed = status, observed
	if status == Pass || status == NotApplicable {
		c.Missing, c.Remediation = "", ""
	}
	return c
}

func resourceValue[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func resourceBool(p *bool) bool {
	return p != nil && *p
}

func resourceKnown(c Check, known, valid bool, observed string) Check {
	if !known {
		return resourceResult(c, Unverified, observed)
	}
	if !valid {
		return resourceResult(c, Fail, observed)
	}
	return resourceResult(c, Pass, observed)
}

func numericVersion(raw string, minimum []int) (bool, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) < len(minimum) || len(parts) > 3 {
		return false, false
	}
	values := make([]int, len(parts))
	for i, part := range parts {
		if part == "" || strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return false, false
		}
		v, err := strconv.Atoi(part)
		if err != nil {
			return false, false
		}
		values[i] = v
	}
	for i, required := range minimum {
		if values[i] != required {
			return true, values[i] > required
		}
	}
	return true, true
}

func agentImage(raw string) (bool, bool) {
	const family = "AKSAzureLinux-V3katagen2-"
	if !strings.HasPrefix(raw, family) {
		return false, false
	}
	return numericVersion(strings.TrimPrefix(raw, family), []int{202608, 14, 0})
}

func vmSizes(pool *armcontainerservice.ManagedClusterAgentPoolProfileProperties) []string {
	var result []string
	if pool.VMSize != nil {
		result = append(result, *pool.VMSize)
	}
	if pool.VirtualMachinesProfile != nil && pool.VirtualMachinesProfile.Scale != nil {
		for _, manual := range pool.VirtualMachinesProfile.Scale.Manual {
			if manual == nil {
				result = append(result, "")
			} else {
				result = append(result, resourceValue(manual.Size))
			}
		}
	}
	for _, actual := range pool.VirtualMachineNodesStatus {
		if actual == nil {
			result = append(result, "")
		} else {
			result = append(result, resourceValue(actual.Size))
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func skuCapacity(
	skus []*armcompute.ResourceSKU, region, size string, cpu, memory float64,
) (Status, string) {
	for _, sku := range skus {
		if sku == nil || !strings.EqualFold(resourceValue(sku.Name), size) ||
			resourceValue(sku.ResourceType) != "virtualMachines" {
			continue
		}
		inRegion := false
		for _, location := range sku.Locations {
			inRegion = inRegion || strings.EqualFold(resourceValue(location), region)
		}
		for _, location := range sku.LocationInfo {
			inRegion = inRegion || location != nil && strings.EqualFold(resourceValue(location.Location), region)
		}
		if !inRegion {
			continue
		}
		capabilities := map[string]float64{}
		for _, capability := range sku.Capabilities {
			if capability == nil {
				continue
			}
			value, err := strconv.ParseFloat(resourceValue(capability.Value), 64)
			if err == nil {
				capabilities[resourceValue(capability.Name)] = value
			}
		}
		cores, hasCPU := capabilities["vCPUs"]
		if available, ok := capabilities["vCPUsAvailable"]; ok {
			cores, hasCPU = available, true
		}
		ram, hasMemory := capabilities["MemoryGB"]
		if !hasCPU || !hasMemory {
			return Unverified, "SKU lacks vCPUs/vCPUsAvailable or MemoryGB capability"
		}
		detail := fmt.Sprintf("%s in %s: %g available vCPU / %g GiB", size, region, cores, ram)
		if !(cores >= cpu && ram >= memory) {
			return Fail, detail
		}
		return Pass, detail
	}
	return Unverified, fmt.Sprintf("no exact virtualMachines SKU capability for %s in %s", size, region)
}

func poolCapacity(
	p *armcontainerservice.ManagedClusterAgentPoolProfileProperties,
	skus []*armcompute.ResourceSKU, region string, cpu, memory float64,
) (Status, string) {
	sizes := vmSizes(p)
	if len(sizes) == 0 {
		return Unverified, "pool has no explicit VM size evidence"
	}
	status, details := Pass, []string{}
	for _, size := range sizes {
		s, detail := skuCapacity(skus, region, size, cpu, memory)
		details = append(details, detail)
		if s == Fail || s == Unverified && status == Pass {
			status = s
		}
	}
	return status, strings.Join(details, "; ")
}

func agentPoolChecks(
	pool *armcontainerservice.AgentPool, skus []*armcompute.ResourceSKU, region string,
) Report {
	p := pool.Properties
	scope := resourceValue(pool.ID)
	if scope == "" {
		scope = resourceValue(pool.Name)
	}
	checks := Report{
		resourceCheck("A06", scope, "one User pool: Azure Linux, KataVmIsolation, count >= 1, >= 8 vCPU / 32 GiB"),
		resourceCheck("A07", scope, "compatible AKSAzureLinux-V3katagen2 image >= 202608.14.0"),
		resourceCheck("A08", scope, "agents.ai.azure.com/sandbox-node=true"),
		resourceCheck("A09", scope, "AgentPool securityProfile.sshAccess=Disabled"),
		resourceCheck("A10", scope, "cluster and required AgentPool child resources provisioningState=Succeeded"),
	}
	knownOS := p.OSSKU != nil
	validOS := resourceValue(p.OSSKU) == "AzureLinux" || resourceValue(p.OSSKU) == "AzureLinux3"
	capacity, detail := poolCapacity(p, skus, region, 8, 32)
	known := knownOS && p.WorkloadRuntime != nil && p.Count != nil && capacity != Unverified
	valid := validOS && resourceValue(p.WorkloadRuntime) == "KataVmIsolation" && p.Count != nil && *p.Count >= 1
	checks[0] = resourceKnown(checks[0], known, valid && capacity == Pass, detail)
	// A known incompatible field is a failure even when a different property is absent.
	if knownOS && !validOS || p.WorkloadRuntime != nil && resourceValue(p.WorkloadRuntime) != "KataVmIsolation" ||
		p.Count != nil && *p.Count < 1 || capacity == Fail {
		checks[0] = resourceResult(checks[0], Fail, "incompatible agent pool configuration; "+detail)
	}
	imageKnown, imageValid := agentImage(resourceValue(p.NodeImageVersion))
	checks[1] = resourceKnown(checks[1], imageKnown, imageValid, resourceValue(p.NodeImageVersion))
	label, labelExists := p.NodeLabels["agents.ai.azure.com/sandbox-node"]
	checks[2] = resourceKnown(checks[2], p.NodeLabels != nil, labelExists && resourceValue(label) == "true",
		"persisted pool sandbox-node label="+resourceValue(label))
	if p.SecurityProfile != nil {
		ssh := resourceValue(p.SecurityProfile.SSHAccess)
		checks[3] = resourceKnown(checks[3], ssh != "", ssh == "Disabled", "sshAccess="+ssh)
	}
	state := resourceValue(p.ProvisioningState)
	checks[4] = resourceKnown(checks[4], state != "", state == "Succeeded", "AgentPool provisioningState="+state)
	return checks
}

func selectedAgentPool(
	cluster armcontainerservice.ManagedCluster, pools []*armcontainerservice.AgentPool, skus []*armcompute.ResourceSKU,
) (*armcontainerservice.AgentPool, Report) {
	var best *armcontainerservice.AgentPool
	var checks Report
	bestScore := -1
	for _, pool := range pools {
		if pool == nil || pool.Properties == nil || resourceValue(pool.Properties.Mode) != "User" {
			continue
		}
		candidate := agentPoolChecks(pool, skus, resourceValue(cluster.Location))
		score := 0
		for _, check := range candidate {
			if check.Status == Pass {
				score += 2
			} else if check.Status == Unverified {
				score++
			}
		}
		if cluster.Properties != nil && cluster.Properties.NetworkProfile != nil {
			n := cluster.Properties.NetworkProfile
			if resourceValue(n.NetworkPlugin) == "azure" &&
				(resourceValue(n.NetworkPluginMode) == "overlay" || resourceValue(pool.Properties.PodSubnetID) != "") {
				score++
			}
		}
		if score > bestScore {
			best, checks, bestScore = pool, candidate, score
		}
	}
	return best, checks
}

func aksResourceChecks(
	cluster armcontainerservice.ManagedCluster, pools []*armcontainerservice.AgentPool, skus []*armcompute.ResourceSKU,
) (string, Report) {
	scope := resourceValue(cluster.ID)
	report := Report{}
	add := func(id, expected string, known, valid bool, observed string) {
		report = append(report, resourceKnown(resourceCheck(id, scope, expected), known, valid, observed))
	}
	p := cluster.Properties
	if p == nil {
		for i := 1; i <= 11; i++ {
			report = append(report, resourceCheck(fmt.Sprintf("A%02d", i), scope, "readable AKS properties"))
		}
		return "", report
	}
	// Base is the documented default SKU name. The billing tier is not the product type.
	sku := "Base"
	if cluster.SKU != nil && cluster.SKU.Name != nil {
		sku = resourceValue(cluster.SKU.Name)
	}
	version := resourceValue(p.CurrentKubernetesVersion)
	knownVersion, validVersion := numericVersion(version, []int{1, 31})
	add("A01", "AKS Base (not Automatic), running Kubernetes >= 1.31",
		knownVersion, sku == "Base" && validVersion, "SKU="+sku+"; currentKubernetesVersion="+version)
	if sku != "Base" {
		report[len(report)-1] = resourceResult(report[len(report)-1], Fail, "unsupported AKS SKU="+sku)
	}
	n := p.NetworkProfile
	selectedPool, selectedChecks := selectedAgentPool(cluster, pools, skus)
	if n == nil {
		add("A02", "Azure CNI Overlay or Pod Subnet", false, false, "networkProfile is absent")
	} else {
		plugin, mode := resourceValue(n.NetworkPlugin), resourceValue(n.NetworkPluginMode)
		podSubnet := false
		if selectedPool != nil {
			podSubnet = resourceValue(selectedPool.Properties.PodSubnetID) != ""
		}
		add("A02", "Azure CNI Overlay or Pod Subnet", plugin != "",
			plugin == "azure" && (mode == "overlay" || podSubnet),
			"plugin="+plugin+"; mode="+mode+"; User pool podSubnetID evidence="+strconv.FormatBool(podSubnet))
	}
	kubelet := ""
	if identity := p.IdentityProfile["kubeletidentity"]; identity != nil {
		kubelet = resourceValue(identity.ObjectID)
	}
	identityType := ""
	if cluster.Identity != nil {
		identityType = resourceValue(cluster.Identity.Type)
	}
	managed := identityType == "SystemAssigned" || identityType == "UserAssigned" ||
		identityType == "SystemAssigned, UserAssigned"
	add("A03", "managed cluster identity and kubelet principal object ID",
		identityType != "" && kubelet != "", managed && kubelet != "",
		"identity.type="+identityType+"; kubelet object ID="+kubelet)
	if identityType != "" && !managed || identityType == "" && p.ServicePrincipalProfile != nil {
		report[len(report)-1] = resourceResult(report[len(report)-1], Fail, "cluster lacks a supported managed identity")
	}
	oidc, workload, issuer := false, false, ""
	knownFederation := p.OidcIssuerProfile != nil && p.SecurityProfile != nil &&
		p.SecurityProfile.WorkloadIdentity != nil
	if p.OidcIssuerProfile != nil {
		oidc, issuer = resourceBool(p.OidcIssuerProfile.Enabled), resourceValue(p.OidcIssuerProfile.IssuerURL)
	}
	if p.SecurityProfile != nil && p.SecurityProfile.WorkloadIdentity != nil {
		workload = resourceBool(p.SecurityProfile.WorkloadIdentity.Enabled)
	}
	add("A04", "OIDC enabled with issuer URL; Workload Identity enabled",
		knownFederation, oidc && workload && strings.TrimSpace(issuer) != "", "OIDC and Workload Identity configuration")
	systemStatus, systemDetails, hasSystem := Pass, []string{}, false
	for _, pool := range pools {
		if pool == nil || pool.Properties == nil {
			continue
		}
		switch resourceValue(pool.Properties.Mode) {
		case "System":
			hasSystem = true
			status, detail := poolCapacity(pool.Properties, skus, resourceValue(cluster.Location), 4, 16)
			systemDetails = append(systemDetails, resourceValue(pool.Name)+": "+detail)
			if status == Fail || status == Unverified && systemStatus == Pass {
				systemStatus = status
			}
		}
	}
	system := resourceCheck("A05", scope, "System pools, every applicable VM size >= 4 vCPU / 16 GiB")
	if !hasSystem {
		systemStatus, systemDetails = Fail, []string{"no System child pool"}
	}
	report = append(report, resourceResult(system, systemStatus, strings.Join(systemDetails, "; ")))
	if selectedPool == nil {
		for _, id := range []string{"A06", "A07", "A08", "A09", "A10"} {
			report = append(report, resourceResult(resourceCheck(id, scope, "one complete qualifying User pool"),
				Fail, "no User child pool"))
		}
	} else {
		report = append(report, selectedChecks...)
	}
	for i := range report {
		if report[i].ID != "A10" {
			continue
		}
		for _, pool := range pools {
			if pool == nil || pool.Properties == nil || resourceValue(pool.Properties.Mode) != "System" {
				continue
			}
			state := resourceValue(pool.Properties.ProvisioningState)
			if state != "Succeeded" {
				report[i] = resourceKnown(report[i], state != "", false, "System child pool state="+state)
			}
		}
		state := resourceValue(p.ProvisioningState)
		if state != "Succeeded" {
			report[i] = resourceKnown(report[i], state != "", false, "cluster provisioningState="+state)
		}
	}
	if n == nil {
		add("A11", "supported network policy engine enabled", false, false, "networkProfile is absent")
	} else {
		policy, dataplane := resourceValue(n.NetworkPolicy), resourceValue(n.NetworkDataplane)
		valid := policy == "azure" || policy == "calico" || dataplane == "cilium" && (policy == "" || policy == "cilium")
		known := policy != "" || dataplane == "azure" || dataplane == "cilium"
		add("A11", "Azure/Calico policy or supported Cilium dataplane",
			known, valid, "networkPolicy="+policy+"; networkDataplane="+dataplane)
	}
	return kubelet, report
}

func storageResourceChecks(account armstorage.Account) Report {
	scope := resourceValue(account.ID)
	typeCheck := resourceCheck("S01", scope, "StorageV2; Hot tier; HNS explicitly disabled")
	security := resourceCheck("S02", scope, "HTTPS only; TLS >= 1.2; Shared Key disabled; anonymous Blob disabled")
	p := account.Properties
	if p == nil {
		return Report{typeCheck, security}
	}
	known := account.Kind != nil && p.AccessTier != nil && p.IsHnsEnabled != nil
	valid := resourceValue(account.Kind) == "StorageV2" && resourceValue(p.AccessTier) == "Hot" &&
		p.IsHnsEnabled != nil && !*p.IsHnsEnabled
	typeCheck = resourceKnown(typeCheck, known, valid,
		"kind="+resourceValue(account.Kind)+"; tier="+resourceValue(p.AccessTier)+"; HNS evidence")
	if account.Kind != nil && resourceValue(account.Kind) != "StorageV2" ||
		p.AccessTier != nil && resourceValue(p.AccessTier) != "Hot" || resourceBool(p.IsHnsEnabled) {
		typeCheck = resourceResult(typeCheck, Fail, "incompatible Storage kind, tier or HNS configuration")
	}
	// Accounts.GetProperties uses API 2024-01-01. Its documented defaults are:
	// HTTPS=true (since 2019-04-01), minimum TLS=1.0, Shared Key=true, anonymous Blob=false.
	https := p.EnableHTTPSTrafficOnly == nil || *p.EnableHTTPSTrafficOnly
	anonymous := resourceBool(p.AllowBlobPublicAccess)
	shared := p.AllowSharedKeyAccess == nil || *p.AllowSharedKeyAccess
	tls := resourceValue(p.MinimumTLSVersion)
	tlsKnown := tls == "" || tls == "TLS1_0" || tls == "TLS1_1" || tls == "TLS1_2" || tls == "TLS1_3"
	tlsValid := tls == "TLS1_2" || tls == "TLS1_3"
	security = resourceKnown(security, tlsKnown, https && !anonymous && !shared && tlsValid,
		fmt.Sprintf("HTTPS=%t; minimum TLS=%s; Shared Key=%t; anonymous Blob=%t", https, tls, shared, anonymous))
	if !https || anonymous || shared || tlsKnown && !tlsValid {
		security = resourceResult(security, Fail, security.Observed)
	}
	return Report{typeCheck, security}
}

func registryResourceCheck(registry registryResource) (bool, Check) {
	c := resourceCheck("C01", resourceValue(registry.ID),
		"ready registry; login endpoint; known access configuration and RBAC/ABAC mode")
	p := registry.Properties
	if p == nil {
		return false, c
	}
	mode := resourceValue(registry.AuthorizationMode)
	abac := mode == "AbacRepositoryPermissions"
	knownMode := mode == "LegacyRegistryPermissions" || abac
	state, endpoint := resourceValue(p.ProvisioningState), resourceValue(p.LoginServer)
	access := resourceValue(p.PublicNetworkAccess)
	knownAccess := access == "Enabled" || access == "Disabled"
	known := knownMode && state != "" && endpoint != "" && knownAccess
	c = resourceKnown(c, known, state == "Succeeded",
		"provisioningState="+state+"; publicNetworkAccess="+access+"; roleAssignmentMode="+mode)
	if state != "" && state != "Succeeded" {
		c = resourceResult(c, Fail, c.Observed)
	}
	if access == "Disabled" && len(p.PrivateEndpointConnections) == 0 {
		c = resourceResult(c, Fail, "public access disabled without a private endpoint connection")
	}
	if access == "Disabled" && len(p.PrivateEndpointConnections) > 0 {
		approved, unknown := false, false
		for _, connection := range p.PrivateEndpointConnections {
			if connection == nil || connection.Properties == nil ||
				connection.Properties.PrivateLinkServiceConnectionState == nil {
				unknown = true
				continue
			}
			status := resourceValue(connection.Properties.PrivateLinkServiceConnectionState.Status)
			approved = approved || status == "Approved"
			unknown = unknown || status == ""
		}
		if !approved {
			status := Fail
			if unknown {
				status = Unverified
			}
			c = resourceResult(c, status, "private-only registry has no verified Approved private endpoint")
		}
	}
	if strings.ContainsAny(endpoint, "/:@?#\\ \r\n\t") {
		c = resourceResult(c, Fail, "registry login server is not a supported hostname")
	}
	return abac, c
}
