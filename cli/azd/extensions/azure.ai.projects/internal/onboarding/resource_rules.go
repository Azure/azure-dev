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
)

func resourceCheck(id, scope, expected string) Check {
	return Check{
		ID: id, Scope: scope, Expected: expected, Status: Unverified,
		Observed: "required ARM evidence is unavailable",
	}
}

func resourceResult(check Check, status Status, observed string) Check {
	check.Status, check.Observed = status, observed
	return check
}

func resourceValue[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func resourceBool(value *bool) bool { return value != nil && *value }

func resourceKnown(check Check, known, valid bool, observed string) Check {
	status := Unverified
	if known {
		status = Fail
		if valid {
			status = Pass
		}
	}
	return resourceResult(check, status, observed)
}

func numericVersion(raw string, minimum []int) (bool, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) < len(minimum) || len(parts) > 3 {
		return false, false
	}
	values := make([]int, len(parts))
	for i, part := range parts {
		if part == "" || strings.IndexFunc(part, func(ch rune) bool { return ch < '0' || ch > '9' }) >= 0 {
			return false, false
		}
		value, err := strconv.Atoi(part)
		if err != nil {
			return false, false
		}
		values[i] = value
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
	var sizes []string
	if pool.VMSize != nil {
		sizes = append(sizes, *pool.VMSize)
	}
	if pool.VirtualMachinesProfile != nil && pool.VirtualMachinesProfile.Scale != nil {
		for _, manual := range pool.VirtualMachinesProfile.Scale.Manual {
			if manual == nil {
				sizes = append(sizes, "")
			} else {
				sizes = append(sizes, resourceValue(manual.Size))
			}
		}
	}
	for _, actual := range pool.VirtualMachineNodesStatus {
		if actual == nil {
			sizes = append(sizes, "")
		} else {
			sizes = append(sizes, resourceValue(actual.Size))
		}
	}
	slices.Sort(sizes)
	return slices.Compact(sizes)
}

func skuCapacity(skus []*armcompute.ResourceSKU, region, size string, cpu, memory float64) (Status, string) {
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
	pool *armcontainerservice.ManagedClusterAgentPoolProfileProperties,
	skus []*armcompute.ResourceSKU, region string, cpu, memory float64,
) (Status, string) {
	sizes := vmSizes(pool)
	if len(sizes) == 0 {
		return Unverified, "pool has no explicit VM size evidence"
	}
	status := Pass
	var details []string
	for _, size := range sizes {
		current, detail := skuCapacity(skus, region, size, cpu, memory)
		details = append(details, detail)
		if current == Fail || current == Unverified && status == Pass {
			status = current
		}
	}
	return status, strings.Join(details, "; ")
}

func agentPoolChecks(pool *armcontainerservice.AgentPool, skus []*armcompute.ResourceSKU, region string) Report {
	p := pool.Properties
	scope := resourceValue(pool.ID)
	checks := Report{
		resourceCheck("A06", scope, "one User pool: Azure Linux, KataVmIsolation, count >= 1, >= 8 vCPU / 32 GiB"),
		resourceCheck("A07", scope, "compatible AKSAzureLinux-V3katagen2 image >= 202608.14.0"),
		resourceCheck("A08", scope, "agents.ai.azure.com/sandbox-node=true"),
		resourceCheck("A09", scope, "AgentPool securityProfile.sshAccess=Disabled"),
		resourceCheck("A10", scope, "cluster and required AgentPool provisioningState=Succeeded"),
	}
	knownOS := p.OSSKU != nil
	validOS := resourceValue(p.OSSKU) == "AzureLinux" || resourceValue(p.OSSKU) == "AzureLinux3"
	capacity, detail := poolCapacity(p, skus, region, 8, 32)
	known := knownOS && p.WorkloadRuntime != nil && p.Count != nil && capacity != Unverified
	valid := validOS && resourceValue(p.WorkloadRuntime) == "KataVmIsolation" && p.Count != nil && *p.Count >= 1
	checks[0] = resourceKnown(checks[0], known, valid && capacity == Pass, detail)
	if knownOS && !validOS || p.WorkloadRuntime != nil && resourceValue(p.WorkloadRuntime) != "KataVmIsolation" ||
		p.Count != nil && *p.Count < 1 || capacity == Fail {
		checks[0] = resourceResult(checks[0], Fail, "incompatible agent pool configuration; "+detail)
	}
	imageKnown, imageValid := agentImage(resourceValue(p.NodeImageVersion))
	checks[1] = resourceKnown(checks[1], imageKnown, imageValid, resourceValue(p.NodeImageVersion))
	label, exists := p.NodeLabels["agents.ai.azure.com/sandbox-node"]
	checks[2] = resourceKnown(checks[2], p.NodeLabels != nil, exists && resourceValue(label) == "true",
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
			network := cluster.Properties.NetworkProfile
			if resourceValue(network.NetworkPlugin) == "azure" &&
				(resourceValue(network.NetworkPluginMode) == "overlay" || resourceValue(pool.Properties.PodSubnetID) != "") {
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
	var report Report
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
	network := p.NetworkProfile
	selected, poolChecks := selectedAgentPool(cluster, pools, skus)
	if network == nil {
		add("A02", "Azure CNI Overlay or Pod Subnet", false, false, "networkProfile is absent")
	} else {
		plugin, mode := resourceValue(network.NetworkPlugin), resourceValue(network.NetworkPluginMode)
		podSubnet := selected != nil && resourceValue(selected.Properties.PodSubnetID) != ""
		add("A02", "Azure CNI Overlay or Pod Subnet", plugin != "", plugin == "azure" && (mode == "overlay" || podSubnet),
			"plugin="+plugin+"; mode="+mode+"; qualifying User pool pod subnet="+strconv.FormatBool(podSubnet))
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
	knownFederation := p.OidcIssuerProfile != nil && p.SecurityProfile != nil && p.SecurityProfile.WorkloadIdentity != nil
	if p.OidcIssuerProfile != nil {
		oidc, issuer = resourceBool(p.OidcIssuerProfile.Enabled), resourceValue(p.OidcIssuerProfile.IssuerURL)
	}
	if p.SecurityProfile != nil && p.SecurityProfile.WorkloadIdentity != nil {
		workload = resourceBool(p.SecurityProfile.WorkloadIdentity.Enabled)
	}
	add("A04", "OIDC enabled with issuer URL; Workload Identity enabled",
		knownFederation, oidc && workload && strings.TrimSpace(issuer) != "", "OIDC and Workload Identity configuration")
	systemStatus, hasSystem := Pass, false
	var details []string
	for _, pool := range pools {
		if pool == nil || pool.Properties == nil || resourceValue(pool.Properties.Mode) != "System" {
			continue
		}
		hasSystem = true
		status, detail := poolCapacity(pool.Properties, skus, resourceValue(cluster.Location), 4, 16)
		details = append(details, resourceValue(pool.Name)+": "+detail)
		if status == Fail || status == Unverified && systemStatus == Pass {
			systemStatus = status
		}
	}
	if !hasSystem {
		systemStatus, details = Fail, []string{"no System child pool"}
	}
	report = append(report, resourceResult(resourceCheck("A05", scope,
		"every System pool VM size >= 4 vCPU / 16 GiB"), systemStatus, strings.Join(details, "; ")))
	if selected == nil {
		for _, id := range []string{"A06", "A07", "A08", "A09", "A10"} {
			report = append(report, resourceResult(resourceCheck(id, scope, "one complete qualifying User pool"),
				Fail, "no User child pool"))
		}
	} else {
		report = append(report, poolChecks...)
	}
	for i := range report {
		if report[i].ID != "A10" {
			continue
		}
		for _, pool := range pools {
			if pool != nil && pool.Properties != nil && resourceValue(pool.Properties.Mode) == "System" {
				state := resourceValue(pool.Properties.ProvisioningState)
				if state != "Succeeded" {
					report[i] = resourceKnown(report[i], state != "", false, "System child pool state="+state)
				}
			}
		}
		if state := resourceValue(p.ProvisioningState); state != "Succeeded" {
			report[i] = resourceKnown(report[i], state != "", false, "cluster provisioningState="+state)
		}
	}
	if network == nil {
		add("A11", "supported network policy engine enabled", false, false, "networkProfile is absent")
	} else {
		policy, dataplane := resourceValue(network.NetworkPolicy), resourceValue(network.NetworkDataplane)
		valid := policy == "azure" || policy == "calico" || dataplane == "cilium" && (policy == "" || policy == "cilium")
		add("A11", "Azure/Calico policy or supported Cilium dataplane",
			policy != "" || dataplane == "azure" || dataplane == "cilium", valid,
			"networkPolicy="+policy+"; networkDataplane="+dataplane)
	}
	return kubelet, report
}
