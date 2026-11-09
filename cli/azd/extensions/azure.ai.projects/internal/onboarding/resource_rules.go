// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore armcompute armstorage GZRS Kata katagen kubelet kubeletidentity OSSKU RAGRS RAGZRS

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

var aksCheckIDs = [...]string{
	"aks-type", "aks-version", "aks-cni", "aks-identity", "aks-federation", "system-pool",
	"agent-pool", "agent-image", "agent-label", "network-policy",
}

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

func poolCapacityWarning(
	pool *armcontainerservice.AgentPool, skus []*armcompute.ResourceSKU, region string, cpu, memory float64,
) string {
	sizes := vmSizes(pool.Properties)
	var details []string
	if len(sizes) == 0 {
		details = append(details, "pool has no explicit VM size evidence")
	}
	for _, size := range sizes {
		status, detail := skuCapacity(skus, region, size, cpu, memory)
		if status != Pass {
			details = append(details, detail)
		}
	}
	if len(details) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"Pool %s: %s. At least %g available vCPU / %g GiB per VM is recommended; "+
			"this recommendation does not block provisioning or change the pool.",
		resourceValue(pool.Name), strings.Join(details, "; "), cpu, memory)
}

func agentPoolChecks(pool *armcontainerservice.AgentPool) Report {
	p := pool.Properties
	scope := resourceValue(pool.ID)
	if scope == "" {
		scope = resourceValue(pool.Name)
	}
	checks := Report{
		resourceCheck("agent-pool", scope, "one User pool: Azure Linux, KataVmIsolation, count >= 1"),
		resourceCheck("agent-image", scope, "compatible AKSAzureLinux-V3katagen2 image >= 202608.14.0"),
		resourceCheck("agent-label", scope, "agents.ai.azure.com/sandbox-node=true"),
	}
	knownOS := p.OSSKU != nil
	validOS := resourceValue(p.OSSKU) == "AzureLinux" || resourceValue(p.OSSKU) == "AzureLinux3"
	known := knownOS && p.WorkloadRuntime != nil && p.Count != nil
	valid := validOS && resourceValue(p.WorkloadRuntime) == "KataVmIsolation" && p.Count != nil && *p.Count >= 1
	detail := "osSKU=" + resourceValue(p.OSSKU) + "; workloadRuntime=" + resourceValue(p.WorkloadRuntime)
	if p.Count != nil {
		detail += "; count=" + strconv.Itoa(int(*p.Count))
	}
	checks[0] = resourceKnown(checks[0], known, valid, detail)
	// A known incompatible field is a failure even when a different property is absent.
	if knownOS && !validOS || p.WorkloadRuntime != nil && resourceValue(p.WorkloadRuntime) != "KataVmIsolation" ||
		p.Count != nil && *p.Count < 1 {
		checks[0] = resourceResult(checks[0], Fail, "incompatible agent pool configuration")
	}
	imageKnown, imageValid := agentImage(resourceValue(p.NodeImageVersion))
	checks[1] = resourceKnown(checks[1], imageKnown, imageValid, resourceValue(p.NodeImageVersion))
	label, labelExists := p.NodeLabels["agents.ai.azure.com/sandbox-node"]
	checks[2] = resourceKnown(checks[2], p.NodeLabels != nil, labelExists && resourceValue(label) == "true",
		"persisted pool sandbox-node label="+resourceValue(label))
	return checks
}

func selectedAgentPool(pools []*armcontainerservice.AgentPool) (*armcontainerservice.AgentPool, Report) {
	var best *armcontainerservice.AgentPool
	var checks Report
	bestScore := -1
	for _, pool := range pools {
		if pool == nil || pool.Properties == nil || resourceValue(pool.Properties.Mode) != "User" {
			continue
		}
		candidate := agentPoolChecks(pool)
		score := 0
		for _, check := range candidate {
			if check.Status == Pass {
				score += 2
			} else if check.Status == Unverified {
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
	cluster armcontainerservice.ManagedCluster, pools []*armcontainerservice.AgentPool, poolsKnown bool,
) (string, Report, *armcontainerservice.AgentPool) {
	scope := resourceValue(cluster.ID)
	selectedPool, selectedChecks := selectedAgentPool(pools)
	report := Report{}
	add := func(id, expected string, known, valid bool, observed string) {
		report = append(report, resourceKnown(resourceCheck(id, scope, expected), known, valid, observed))
	}
	p := cluster.Properties
	if p == nil {
		for _, id := range aksCheckIDs {
			report = append(report, resourceCheck(id, scope, "readable AKS properties"))
		}
		return "", report, selectedPool
	}
	skuName := ""
	if cluster.SKU != nil {
		skuName = resourceValue(cluster.SKU.Name)
	}
	typeCheck := resourceKnown(
		resourceCheck("aks-type", scope, "AKS Standard cluster (sku.name=Base)"),
		skuName != "",
		strings.EqualFold(skuName, "Base"),
		fmt.Sprintf("returned cluster sku.name=%q", skuName),
	)
	if typeCheck.Status != Pass {
		typeCheck.Missing = "explicit sku.name=Base on the selected AKS cluster"
		typeCheck.Remediation = "Select an AKS Standard cluster with sku.name=Base. " +
			"AKS Automatic is not supported by this hosting configuration."
	}
	report = append(report, typeCheck)
	version := resourceValue(p.CurrentKubernetesVersion)
	knownVersion, validVersion := numericVersion(version, []int{1, 31})
	add("aks-version", "running Kubernetes >= 1.31",
		knownVersion, validVersion, "currentKubernetesVersion="+version)
	n := p.NetworkProfile
	if n == nil {
		add("aks-cni", "Azure CNI Overlay or Pod Subnet on every pool", false, false, "networkProfile is absent")
	} else {
		plugin := strings.ToLower(resourceValue(n.NetworkPlugin))
		mode := strings.ToLower(resourceValue(n.NetworkPluginMode))
		allPodSubnets := len(p.AgentPoolProfiles) > 0
		for _, pool := range p.AgentPoolProfiles {
			if pool == nil || resourceValue(pool.PodSubnetID) == "" {
				allPodSubnets = false
				break
			}
		}
		add("aks-cni", "Azure CNI Overlay or Pod Subnet on every pool", plugin != "",
			plugin == "azure" && (mode == "overlay" || allPodSubnets),
			"plugin="+plugin+"; mode="+mode+"; nonempty pool list with all podSubnetID="+strconv.FormatBool(allPodSubnets))
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
	add("aks-identity", "managed cluster identity and kubelet principal object ID",
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
	add("aks-federation", "OIDC enabled with issuer URL; Workload Identity enabled",
		knownFederation, oidc && workload && strings.TrimSpace(issuer) != "", "OIDC and Workload Identity configuration")
	hasSystem := false
	for _, pool := range pools {
		if pool == nil || pool.Properties == nil {
			continue
		}
		if resourceValue(pool.Properties.Mode) == "System" {
			hasSystem = true
		}
	}
	systemObserved := "AKS child pools could not be read"
	if poolsKnown {
		systemObserved = "System child pool present=" + strconv.FormatBool(hasSystem)
	}
	add("system-pool", "at least one System child pool", poolsKnown, hasSystem, systemObserved)
	if selectedPool == nil {
		for _, id := range []string{"agent-pool", "agent-image", "agent-label"} {
			check := resourceCheck(id, scope, "one complete qualifying User pool")
			if poolsKnown {
				check = resourceResult(check, Fail, "no User child pool")
			} else {
				check.Observed = "AKS child pools could not be read"
			}
			report = append(report, check)
		}
	} else {
		report = append(report, selectedChecks...)
	}
	if n == nil {
		add("network-policy", "supported network policy engine enabled", false, false, "networkProfile is absent")
	} else {
		policy, dataplane := resourceValue(n.NetworkPolicy), resourceValue(n.NetworkDataplane)
		valid := policy == "azure" || policy == "calico" || dataplane == "cilium" && (policy == "" || policy == "cilium")
		known := policy != "" || dataplane == "azure" || dataplane == "cilium"
		add("network-policy", "Azure/Calico policy or supported Cilium dataplane",
			known, valid, "networkPolicy="+policy+"; networkDataplane="+dataplane)
	}
	return kubelet, report, selectedPool
}

func storageResourceChecks(account armstorage.Account) Report {
	scope := resourceValue(account.ID)
	typeCheck := resourceCheck("storage-type", scope, "StorageV2; Hot tier; HNS explicitly disabled")
	security := resourceCheck("storage-security", scope, "HTTPS only; TLS >= 1.2")
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
	sku := ""
	if account.SKU != nil {
		sku = resourceValue(account.SKU.Name)
	}
	typeCheck.Observed += "; SKU=" + sku
	switch strings.ToUpper(strings.TrimSpace(sku)) {
	case "STANDARD_ZRS", "STANDARD_GZRS", "STANDARD_RAGZRS", "PREMIUM_ZRS":
	case "STANDARD_LRS", "STANDARD_GRS", "STANDARD_RAGRS", "PREMIUM_LRS":
		typeCheck.Warning = fmt.Sprintf(
			"Supplied Storage account uses %s without zone redundancy. "+
				"Zone redundancy is recommended; this recommendation does not block provisioning or change the account.",
			sku)
	default:
		typeCheck.Warning = "The supplied Storage account SKU does not establish zone redundancy. " +
			"Zone redundancy is recommended; confirm the account's redundancy setting. " +
			"This recommendation does not block provisioning or change the account."
	}
	// Accounts.GetProperties uses API 2024-01-01. Its documented defaults are:
	// HTTPS=true (since 2019-04-01), minimum TLS=1.0.
	https := p.EnableHTTPSTrafficOnly == nil || *p.EnableHTTPSTrafficOnly
	tls := resourceValue(p.MinimumTLSVersion)
	tlsKnown := tls == "" || tls == "TLS1_0" || tls == "TLS1_1" || tls == "TLS1_2" || tls == "TLS1_3"
	tlsValid := tls == "TLS1_2" || tls == "TLS1_3"
	security = resourceKnown(security, tlsKnown, https && tlsValid,
		fmt.Sprintf("HTTPS=%t; minimum TLS=%s", https, tls))
	if !https || tlsKnown && !tlsValid {
		security = resourceResult(security, Fail, security.Observed)
	}
	return Report{typeCheck, security}
}
