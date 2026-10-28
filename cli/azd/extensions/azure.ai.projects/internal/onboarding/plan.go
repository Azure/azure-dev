// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/google/uuid"
)

// Resource is an exact target and its original create/reference selection.
type Resource struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Supplied bool   `json:"supplied"`
}

// Network retains the standard Foundry network selections.
type Network struct {
	VNetID            string `json:"vnetId"`
	AgentSubnetName   string `json:"agentSubnetName"`
	AgentSubnetPrefix string `json:"agentSubnetPrefix"`
	PESubnetName      string `json:"peSubnetName"`
	PESubnetPrefix    string `json:"peSubnetPrefix"`
	CreateAgentSubnet bool   `json:"createAgentSubnet"`
	CreatePESubnet    bool   `json:"createPESubnet"`
	DNSResourceGroup  string `json:"dnsResourceGroup"`
	DNSSubscription   string `json:"dnsSubscription"`
}

// Validate rejects locally detectable network conflicts before credential or resource reads.
func (n Network) Validate() error {
	if _, err := ParseID(n.VNetID, "Microsoft.Network/virtualNetworks"); err != nil {
		return err
	}
	for _, name := range []string{n.AgentSubnetName, n.PESubnetName} {
		if name == "" || strings.ContainsAny(name, "/\\?#%${} \t\r\n") {
			return fmt.Errorf("network.agentSubnet and network.peSubnet require explicit valid names")
		}
	}
	if strings.EqualFold(n.AgentSubnetName, n.PESubnetName) {
		return fmt.Errorf("network.agentSubnet.name and network.peSubnet.name must differ")
	}
	var prefixes []netip.Prefix
	for _, subnet := range []struct {
		name, prefix  string
		create, agent bool
	}{
		{"agentSubnet", n.AgentSubnetPrefix, n.CreateAgentSubnet, true},
		{"peSubnet", n.PESubnetPrefix, n.CreatePESubnet, false},
	} {
		if subnet.create != (subnet.prefix != "") {
			return fmt.Errorf("network.%s creation requires an explicit prefix; reference mode cannot apply a prefix",
				subnet.name)
		}
		if !subnet.create {
			continue
		}
		prefix, err := netip.ParsePrefix(subnet.prefix)
		if err != nil || prefix != prefix.Masked() || !prefix.Addr().Is4() {
			return fmt.Errorf("network.%s.prefix requires a canonical IPv4 CIDR", subnet.name)
		}
		if subnet.agent && (prefix.Bits() > 27 || !prefix.Addr().IsPrivate() || !prefixLastAddress(prefix).IsPrivate()) {
			return fmt.Errorf("network.agentSubnet.prefix requires a private IPv4 /27 or larger")
		}
		prefixes = append(prefixes, prefix)
	}
	if len(prefixes) == 2 && prefixes[0].Overlaps(prefixes[1]) {
		return fmt.Errorf("network.agentSubnet and network.peSubnet prefixes must not overlap")
	}
	if n.DNSResourceGroup == "" && n.DNSSubscription != "" {
		return fmt.Errorf("network.dns.subscription requires network.dns.resourceGroup")
	}
	if len(n.DNSResourceGroup) > 90 || strings.ContainsAny(n.DNSResourceGroup, "/\\?#%${} \t\r\n") ||
		strings.HasSuffix(n.DNSResourceGroup, ".") {
		return fmt.Errorf("network.dns.resourceGroup is invalid")
	}
	if n.DNSSubscription != "" {
		if _, err := uuid.Parse(n.DNSSubscription); err != nil {
			return fmt.Errorf("network.dns.subscription must be a subscription GUID")
		}
	}
	return nil
}

// Plan binds the existing cluster to automatically created supporting resources.
type Plan struct {
	SubscriptionID        string   `json:"subscriptionId"`
	ResourceGroup         string   `json:"resourceGroup"`
	ResourceGroupSupplied bool     `json:"resourceGroupSupplied"`
	Location              string   `json:"location"`
	Environment           string   `json:"environment"`
	DeploymentID          string   `json:"deploymentId"`
	Account               Resource `json:"account"`
	Project               Resource `json:"project"`
	Cluster               Resource `json:"cluster"`
	Storage               Resource `json:"storage"`
	Registry              Resource `json:"registry"`
	Hosting               Resource `json:"hosting"`
	Workload              Resource `json:"workload"`
	Network               Network  `json:"network"`
}

// Input contains resolved values from the standard template and azure.yaml.
type Input struct {
	ClusterID, SubscriptionID, ResourceGroup, Location, Environment string
	AccountID, ProjectName, RegistryName, DeploymentID              string
	ResourceGroupSupplied                                           bool
	Network                                                         Network
}

// ParseID requires an exact ARM resource type and complete subscription/RG hierarchy.
func ParseID(value, kind string) (*arm.ResourceID, error) {
	if !strings.HasPrefix(strings.ToLower(value), "/subscriptions/") ||
		strings.ContainsAny(value, "?#%\\@:\x00 \t\r\n") || strings.Contains(value, "//") {
		return nil, fmt.Errorf("expected a complete %s resource ID without URL components", kind)
	}
	id, err := arm.ParseResourceID(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s resource ID: %w", kind, err)
	}
	if id.SubscriptionID == "" || id.ResourceGroupName == "" || id.Name == "" ||
		!strings.EqualFold(id.ResourceType.String(), kind) {
		return nil, fmt.Errorf("expected a complete %s resource ID", kind)
	}
	return id, nil
}

// BuildPlan uses the native Foundry/ACR names and deterministic prerequisite names.
func BuildPlan(in Input) (Plan, error) {
	cluster, err := ParseID(in.ClusterID, "Microsoft.ContainerService/managedClusters")
	if err != nil {
		return Plan{}, err
	}
	account, err := ParseID(in.AccountID, "Microsoft.CognitiveServices/accounts")
	if err != nil || !strings.EqualFold(account.SubscriptionID, in.SubscriptionID) ||
		!strings.EqualFold(account.ResourceGroupName, in.ResourceGroup) {
		return Plan{}, fmt.Errorf("the exact Foundry account must belong to the selected subscription and resource group")
	}
	deployment, err := arm.ParseResourceID(in.DeploymentID)
	if err != nil || !strings.EqualFold(deployment.SubscriptionID, in.SubscriptionID) ||
		deployment.ResourceGroupName != "" || deployment.Name == "" ||
		!strings.EqualFold(deployment.ResourceType.String(), "Microsoft.Resources/deployments") ||
		strings.ContainsAny(in.DeploymentID, "?#%\\@:\x00 \t\r\n") {
		return Plan{}, fmt.Errorf("the exact subscription deployment ID is required")
	}
	if in.Location == "" || in.Environment == "" || in.ProjectName == "" ||
		strings.ContainsAny(in.ProjectName, "/\\?#% \t\r\n") ||
		len(in.RegistryName) < 5 || len(in.RegistryName) > 50 ||
		strings.IndexFunc(in.RegistryName, func(ch rune) bool {
			return !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9')
		}) >= 0 {
		return Plan{}, fmt.Errorf("resolved location, environment, project and native registry name are required")
	}
	if err := in.Network.Validate(); err != nil {
		return Plan{}, err
	}
	var prefix strings.Builder
	for _, ch := range strings.ToLower(account.Name) {
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' {
			prefix.WriteRune(ch)
			if prefix.Len() == 14 {
				break
			}
		}
	}
	if prefix.Len() == 0 {
		return Plan{}, fmt.Errorf("Foundry account name needs an ASCII letter or digit")
	}
	parent := account.Parent.String()
	owned := func(kind, name string) Resource {
		return Resource{ID: parent + "/providers/" + kind + "/" + name, Name: name}
	}
	hash := sha256.Sum256([]byte(strings.ToLower(in.AccountID)))
	storageName := prefix.String() + hex.EncodeToString(hash[:])[:10]
	return Plan{
		SubscriptionID: in.SubscriptionID, ResourceGroup: in.ResourceGroup,
		ResourceGroupSupplied: in.ResourceGroupSupplied,
		Location:              in.Location, Environment: in.Environment, DeploymentID: in.DeploymentID,
		Account:  Resource{ID: in.AccountID, Name: account.Name},
		Project:  Resource{ID: in.AccountID + "/projects/" + in.ProjectName, Name: in.ProjectName},
		Cluster:  Resource{ID: in.ClusterID, Name: cluster.Name, Supplied: true},
		Storage:  owned("Microsoft.Storage/storageAccounts", storageName),
		Registry: owned("Microsoft.ContainerRegistry/registries", in.RegistryName),
		Hosting:  owned("Microsoft.ManagedIdentity/userAssignedIdentities", strings.ToLower(account.Name)+"-hosting-mi"),
		Workload: owned("Microsoft.ManagedIdentity/userAssignedIdentities", strings.ToLower(account.Name)+"-workload-mi"),
		Network:  in.Network,
	}, nil
}

// StorageSKU selects ZRS except in the public regions without StorageV2 ZRS support.
func StorageSKU(location string) string {
	unsupported := []string{
		"australiacentral", "australiacentral2", "australiasoutheast", "brazilsoutheast", "canadaeast",
		"francesouth", "germanynorth", "koreasouth", "northcentralus", "norwaywest", "southafricawest",
		"southindia", "swedensouth", "switzerlandwest", "taiwannorth", "taiwannorthwest", "uaecentral",
		"ukwest", "westcentralus", "westindia", "westus",
	}
	if slices.Contains(unsupported, strings.ToLower(strings.TrimSpace(location))) {
		return "Standard_LRS"
	}
	return "Standard_ZRS"
}

// Digest hashes a stable JSON binding for the write-ahead intent.
func Digest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode BYO AKS binding: %w", err)
	}
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return "", fmt.Errorf("normalize BYO AKS binding: %w", err)
	}
	raw, err = json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode normalized BYO AKS binding: %w", err)
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}
