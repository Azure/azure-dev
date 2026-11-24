// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore armstorage Kata katagen kubelet kubeletidentity OSSKU

package onboarding

import (
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v9"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
)

const (
	aksResourceType              = "AKS"
	storageResourceType          = "Storage account"
	hostingIdentityResourceType  = "Hosting managed identity"
	workloadIdentityResourceType = "Workload managed identity"
	networkResourceType          = "Agent network"
	subscriptionResourceType     = "Subscription"
)

var aksCheckIDs = [...]string{
	"aks-type", "aks-version", "aks-cni", "aks-identity", "aks-federation", "system-pool",
	"agent-pool", "agent-image", "agent-label", "network-policy",
}

func resourceCheck(id, scope, resourceType string, reasons ...string) Check {
	return Check{
		ID: id, Scope: scope, ResourceType: resourceType, Status: Unverified, Mandatory: true, FailureReasons: reasons,
	}
}

func resourceResult(c Check, status Status, reasons ...string) Check {
	c.Status = status
	if status == Pass || status == NotApplicable {
		c.FailureReasons = nil
	} else if len(reasons) > 0 {
		c.FailureReasons = reasons
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

func resourceKnown(c Check, known, valid bool, failedReasons ...string) Check {
	if !known {
		return resourceResult(c, Unverified)
	}
	if !valid {
		return resourceResult(c, Fail, failedReasons...)
	}
	return resourceResult(c, Pass)
}

func addFailureReason(c *Check, known, valid bool, unknown, failed string) {
	if !known {
		c.FailureReasons = append(c.FailureReasons, unknown)
	} else if !valid {
		c.FailureReasons = append(c.FailureReasons, failed)
	}
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

func agentPoolChecks(pool *armcontainerservice.AgentPool) Report {
	p := pool.Properties
	scope := resourceValue(pool.ID)
	if scope == "" {
		scope = resourceValue(pool.Name)
	}
	checks := Report{
		resourceCheck("agent-pool", scope, aksResourceType),
		resourceCheck("agent-image", scope, aksResourceType,
			"The selected User node pool's Kata image could not be verified"),
		resourceCheck("agent-label", scope, aksResourceType),
	}
	knownOS := p.OSSKU != nil
	validOS := resourceValue(p.OSSKU) == "AzureLinux" || resourceValue(p.OSSKU) == "AzureLinux3"
	knownRuntime := p.WorkloadRuntime != nil
	validRuntime := resourceValue(p.WorkloadRuntime) == "KataVmIsolation"
	knownCount := p.Count != nil
	validCount := knownCount && *p.Count >= 1
	known := knownOS && knownRuntime && knownCount
	valid := validOS && validRuntime && validCount
	addFailureReason(&checks[0], knownOS, validOS,
		"The selected User node pool's operating system could not be verified",
		"The selected User node pool does not use Azure Linux")
	addFailureReason(&checks[0], knownRuntime, validRuntime,
		"The selected User node pool's Kata runtime could not be verified",
		"The selected User node pool is not Kata-enabled")
	addFailureReason(&checks[0], knownCount, validCount,
		"The selected User node pool's node count could not be verified",
		"The selected User node pool has no nodes")
	checks[0] = resourceKnown(checks[0], known, valid)
	// A known incompatible field is a failure even when a different property is absent.
	if knownOS && !validOS || knownRuntime && !validRuntime || knownCount && !validCount {
		checks[0] = resourceResult(checks[0], Fail)
	}
	imageKnown, imageValid := agentImage(resourceValue(p.NodeImageVersion))
	checks[1] = resourceKnown(checks[1], imageKnown, imageValid,
		"The selected User node pool's Kata image is older than the minimum supported version")
	label, labelExists := p.NodeLabels["agents.ai.azure.com/sandbox-node"]
	checks[2] = resourceKnown(checks[2], true, labelExists && resourceValue(label) == "true",
		"Sandbox label was not detected as enabled on the selected User node pool")
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
	selectedPool *armcontainerservice.AgentPool,
) (string, Report) {
	scope := resourceValue(cluster.ID)
	report := Report{}
	add := func(id string, known, valid bool, unknown, failed string) {
		report = append(report, resourceKnown(resourceCheck(id, scope, aksResourceType, unknown), known, valid, failed))
	}
	p := cluster.Properties
	if p == nil {
		for _, id := range aksCheckIDs {
			report = append(report, resourceCheck(id, scope, aksResourceType, "AKS configuration could not be read"))
		}
		return "", report
	}
	skuName := ""
	if cluster.SKU != nil {
		skuName = resourceValue(cluster.SKU.Name)
	}
	typeCheck := resourceKnown(
		resourceCheck("aks-type", scope, aksResourceType, "AKS Standard cluster type could not be verified"),
		skuName != "",
		strings.EqualFold(skuName, "Base"),
		"AKS Standard is required; the cluster does not use the supported Base SKU",
	)
	report = append(report, typeCheck)
	version := resourceValue(p.CurrentKubernetesVersion)
	knownVersion, validVersion := numericVersion(version, []int{1, 31})
	add("aks-version", knownVersion, validVersion, "The running Kubernetes version could not be verified",
		"The running Kubernetes version is below the required minimum of 1.31")
	n := p.NetworkProfile
	if n == nil {
		add("aks-cni", false, false, "Azure CNI configuration could not be verified", "")
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
		add("aks-cni", plugin != "", plugin == "azure" && (mode == "overlay" || allPodSubnets),
			"Azure CNI configuration could not be verified",
			"Azure CNI Overlay or Pod Subnet on every node pool is required")
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
	identityCheck := resourceCheck("aks-identity", scope, aksResourceType)
	addFailureReason(&identityCheck, identityType != "" || p.ServicePrincipalProfile != nil, managed,
		"Cluster managed identity could not be verified", "Cluster managed identity is not enabled")
	addFailureReason(&identityCheck, kubelet != "", kubelet != "",
		"Kubelet identity principal could not be verified", "")
	identityCheck = resourceKnown(identityCheck, identityType != "" && kubelet != "", managed && kubelet != "")
	if identityType != "" && !managed || identityType == "" && p.ServicePrincipalProfile != nil {
		identityCheck = resourceResult(identityCheck, Fail)
	}
	report = append(report, identityCheck)
	oidc := p.OidcIssuerProfile
	oidcKnown := oidc != nil && oidc.Enabled != nil
	oidcEnabled := oidcKnown && *oidc.Enabled
	issuerKnown := oidc != nil && oidc.IssuerURL != nil
	issuerPresent := issuerKnown && strings.TrimSpace(*oidc.IssuerURL) != ""
	workloadKnown := p.SecurityProfile != nil && p.SecurityProfile.WorkloadIdentity != nil &&
		p.SecurityProfile.WorkloadIdentity.Enabled != nil
	workloadEnabled := workloadKnown && *p.SecurityProfile.WorkloadIdentity.Enabled
	federation := resourceCheck("aks-federation", scope, aksResourceType)
	addFailureReason(&federation, oidcKnown, oidcEnabled, "OIDC enablement could not be verified", "OIDC is not enabled")
	addFailureReason(&federation, issuerKnown, issuerPresent, "OIDC issuer URL could not be verified",
		"OIDC issuer URL is missing")
	addFailureReason(&federation, workloadKnown, workloadEnabled, "Workload identity enablement could not be verified",
		"Workload identity is not enabled")
	federation = resourceKnown(federation, oidcKnown && workloadKnown && issuerKnown,
		oidcEnabled && workloadEnabled && issuerPresent)
	if oidcKnown && !oidcEnabled || workloadKnown && !workloadEnabled || issuerKnown && !issuerPresent {
		federation = resourceResult(federation, Fail)
	}
	report = append(report, federation)
	hasSystem := false
	for _, pool := range pools {
		if pool == nil || pool.Properties == nil {
			continue
		}
		if resourceValue(pool.Properties.Mode) == "System" {
			hasSystem = true
		}
	}
	add("system-pool", poolsKnown, hasSystem, "Node pools could not be read", "No System node pool found")
	if selectedPool == nil {
		for _, id := range []string{"agent-pool", "agent-image", "agent-label"} {
			check := resourceCheck(id, scope, aksResourceType, "Node pools could not be read")
			if poolsKnown {
				check = resourceResult(check, Fail, "No User node pool found")
			}
			report = append(report, check)
		}
	} else {
		report = append(report, agentPoolChecks(selectedPool)...)
	}
	if n == nil {
		add("network-policy", false, false, "Network policy configuration could not be verified", "")
	} else {
		policy, dataplane := resourceValue(n.NetworkPolicy), resourceValue(n.NetworkDataplane)
		valid := policy == "azure" || policy == "calico" || dataplane == "cilium" && (policy == "" || policy == "cilium")
		known := policy != "" || dataplane == "azure" || dataplane == "cilium"
		add("network-policy", known, valid, "Network policy configuration could not be verified",
			"A supported Azure, Calico or Cilium network policy is not enabled")
	}
	return kubelet, report
}

func storageResourceChecks(account armstorage.Account) Report {
	scope := resourceValue(account.ID)
	typeCheck := resourceCheck("storage-type", scope, storageResourceType, "Storage configuration could not be read")
	security := resourceCheck("storage-security", scope, storageResourceType, "Storage configuration could not be read")
	p := account.Properties
	if p == nil {
		return Report{typeCheck, security}
	}
	known := account.Kind != nil && p.AccessTier != nil
	valid := resourceValue(account.Kind) == "StorageV2" && resourceValue(p.AccessTier) == "Hot" &&
		!resourceBool(p.IsHnsEnabled)
	typeCheck.FailureReasons = nil
	addFailureReason(&typeCheck, account.Kind != nil, resourceValue(account.Kind) == "StorageV2",
		"Storage account kind could not be verified", "StorageV2 account kind is required")
	addFailureReason(&typeCheck, p.AccessTier != nil, resourceValue(p.AccessTier) == "Hot",
		"Storage access tier could not be verified", "Hot storage access tier is required")
	addFailureReason(&typeCheck, true, !resourceBool(p.IsHnsEnabled), "", "Hierarchical namespace must be disabled")
	typeCheck = resourceKnown(typeCheck, known, valid)
	if account.Kind != nil && resourceValue(account.Kind) != "StorageV2" ||
		p.AccessTier != nil && resourceValue(p.AccessTier) != "Hot" || resourceBool(p.IsHnsEnabled) {
		typeCheck = resourceResult(typeCheck, Fail)
	}
	// Accounts.GetProperties uses API 2024-01-01. Its documented defaults are:
	// HTTPS=true (since 2019-04-01), minimum TLS=1.0.
	https := p.EnableHTTPSTrafficOnly == nil || *p.EnableHTTPSTrafficOnly
	tls := resourceValue(p.MinimumTLSVersion)
	tlsKnown := tls == "" || tls == "TLS1_0" || tls == "TLS1_1" || tls == "TLS1_2" || tls == "TLS1_3"
	tlsValid := tls == "TLS1_2" || tls == "TLS1_3"
	security.FailureReasons = nil
	addFailureReason(&security, true, https, "", "HTTPS-only traffic is not enabled")
	addFailureReason(&security, tlsKnown, tlsValid, "Minimum TLS version could not be verified",
		"Minimum TLS version must be at least 1.2")
	security = resourceKnown(security, tlsKnown, https && tlsValid)
	if !https || tlsKnown && !tlsValid {
		security = resourceResult(security, Fail)
	}
	return Report{typeCheck, security}
}
