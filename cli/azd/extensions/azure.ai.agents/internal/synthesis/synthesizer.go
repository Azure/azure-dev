// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package synthesis turns the body of a Foundry service in azure.yaml
// into the inputs needed to compile an ARM template in memory:
//
//   - the embedded main.bicep + modules tree, ready to be staged on disk
//     for the bicep compiler
//   - a Parameters map of the values the template's params consume
//
// Greenfield only: if the service has an endpoint: field, ErrEndpointBrownfield
// is returned so callers can short-circuit the provision path.
package synthesis

import (
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"go.yaml.in/yaml/v3"
)

// Sentinel errors returned by Synthesize.
var (
	// ErrEndpointBrownfield indicates the service points at an existing
	// Foundry project via endpoint:. The provider should skip ARM
	// provisioning and connect to the endpoint directly.
	ErrEndpointBrownfield = errors.New("synthesis: service has endpoint: (brownfield)")

	// ErrServiceNotFound indicates the requested service does not exist
	// in azure.yaml or its host: value is not in AcceptedHosts.
	ErrServiceNotFound = errors.New("synthesis: service not found or host not accepted")
)

// Input is the synthesizer's view of azure.yaml.
type Input struct {
	// RawAzureYAML is the full bytes of azure.yaml.
	RawAzureYAML []byte

	// ServiceName is the key under services: to synthesize for
	// (e.g. "my-project").
	ServiceName string

	// AcceptedHosts lists the values of `services.<name>.host` the
	// caller treats as a Foundry service. If empty, the service's host
	// value is not checked (only existence and endpoint: are).
	AcceptedHosts []string

	// Env maps azd environment variable names to values. Used to resolve
	// ${VAR} references in network fields (subnet vnet ids, dns.subscription).
	// When a referenced variable is absent here, the synthesizer falls back
	// to the process environment before failing. May be nil.
	Env map[string]string

	// PreserveVarRefs keeps ${VAR} references verbatim instead of resolving
	// them. Used by the eject path, where the synthesized main.parameters.json
	// must stay environment-portable: the on-disk provision flow resolves
	// ${VAR} from the azd environment at provision time. When false (the
	// provision path), ${VAR} is resolved here and a missing variable fails.
	PreserveVarRefs bool

	// ProjectRoot is the directory holding azure.yaml. When set, $ref file
	// includes in the service entry (and its deployment items) are resolved
	// against it before synthesis, so refs become the actual content rather
	// than zero-valued params. Empty disables resolution.
	ProjectRoot string
}

// Result bundles the bicep sources and the parameter values derived
// from the service body. Callers stage Templates on disk, compile
// main.bicep, and pass Parameters when invoking the resulting ARM
// deployment.
type Result struct {
	// Parameters maps bicep param names to plain Go values. Callers wrap
	// these in ARM's {"value": ...} envelope when serializing.
	Parameters map[string]any

	// NetworkMode is "none", "byo", or "managed" — derived from the
	// network: block (or its absence). Exposed for telemetry.
	NetworkMode string
}

// Deployment mirrors the deploymentType in main.bicep.
type Deployment struct {
	Name  string          `yaml:"name" json:"name"`
	Model DeploymentModel `yaml:"model" json:"model"`
	Sku   DeploymentSku   `yaml:"sku" json:"sku"`
}

// DeploymentModel mirrors the model field of deploymentType.
type DeploymentModel struct {
	Name    string `yaml:"name" json:"name"`
	Format  string `yaml:"format" json:"format"`
	Version string `yaml:"version" json:"version"`
}

// DeploymentSku mirrors the sku field of deploymentType.
type DeploymentSku struct {
	Name     string `yaml:"name" json:"name"`
	Capacity int    `yaml:"capacity" json:"capacity"`
}

// dockerBlock is the subset of a docker: object we read to decide whether a registry is needed.
type dockerBlock struct {
	Path string `yaml:"path"`
}

// agentBlock is the subset of a legacy inline agent entry we inspect.
type agentBlock struct {
	Name   string       `yaml:"name"`
	Docker *dockerBlock `yaml:"docker,omitempty"`
	Image  string       `yaml:"image,omitempty"`
}

// serviceBlock is the subset of a service entry we inspect for cross-service provisioning inputs.
type serviceBlock struct {
	Host   string       `yaml:"host"`
	Docker *dockerBlock `yaml:"docker,omitempty"`
	Image  string       `yaml:"image,omitempty"`
	Agents []agentBlock `yaml:"agents,omitempty"`
}

// projectService is the subset of a host: azure.ai.project service body the synthesizer reads.
// Unknown fields are intentionally ignored: they are reconciled in deploy-time service targets.
type projectService struct {
	Host        string        `yaml:"host"`
	Endpoint    string        `yaml:"endpoint,omitempty"`
	Deployments []Deployment  `yaml:"deployments,omitempty"`
	Agents      []agentBlock  `yaml:"agents,omitempty"`
	Network     *networkBlock `yaml:"network,omitempty"`
}

// networkBlock mirrors the network: sub-tree on the service body.
//
// The block models two orthogonal axes:
//
//   - Egress (agent runtime network): agentSubnet present injects the agent into
//     that customer subnet; agentSubnet absent uses the Microsoft-managed
//     network. isolationMode tunes the managed network's outbound posture and is
//     valid only when agentSubnet is absent.
//   - Ingress (account data plane): peSubnet is required and always yields an
//     account private endpoint, so a network-bound account is never public.
type networkBlock struct {
	AgentSubnet   *subnetSpec `yaml:"agentSubnet,omitempty"`
	IsolationMode string      `yaml:"isolationMode,omitempty"`
	PESubnet      *subnetSpec `yaml:"peSubnet,omitempty"`
	DNS           *dnsBlock   `yaml:"dns,omitempty"`
}

// subnetSpec is a self-contained subnet descriptor: vnet + name identify the
// subnet, and the optional prefix toggles create-vs-reference.
//
//	vnet + name           -> reference the existing subnet
//	vnet + name + prefix  -> create the subnet with that CIDR
type subnetSpec struct {
	VNet   string `yaml:"vnet,omitempty"`
	Name   string `yaml:"name,omitempty"`
	Prefix string `yaml:"prefix,omitempty"`
}

// dnsBlock mirrors network.dns (private DNS zone references).
type dnsBlock struct {
	ResourceGroup string `yaml:"resourceGroup,omitempty"`
	Subscription  string `yaml:"subscription,omitempty"`
}

// projectFile is the root of azure.yaml as we care about it: only services.
type projectFile struct {
	Services map[string]yaml.Node `yaml:"services"`
}

// Synthesize derives the parameter values needed by main.bicep from one
// Foundry project service in azure.yaml.
func Synthesize(in Input) (*Result, error) {
	if len(in.RawAzureYAML) == 0 {
		return nil, errors.New("synthesis: RawAzureYAML is empty")
	}
	if in.ServiceName == "" {
		return nil, errors.New("synthesis: ServiceName is empty")
	}

	var root projectFile
	if err := yaml.Unmarshal(in.RawAzureYAML, &root); err != nil {
		return nil, fmt.Errorf("parse azure.yaml: %w", err)
	}

	node, ok := root.Services[in.ServiceName]
	if !ok {
		return nil, ErrServiceNotFound
	}

	// Resolve $ref file includes (service-entry-level and per-deployment) so the
	// decoded service body carries the referenced content, not raw $ref objects.
	if in.ProjectRoot != "" {
		var err error
		node, err = resolveServiceRefs(node, in.ProjectRoot, in.ServiceName)
		if err != nil {
			return nil, err
		}
	}

	var svc projectService
	if err := node.Decode(&svc); err != nil {
		return nil, fmt.Errorf("decode service %q: %w", in.ServiceName, err)
	}

	if len(in.AcceptedHosts) > 0 && !slices.Contains(in.AcceptedHosts, svc.Host) {
		return nil, ErrServiceNotFound
	}
	if strings.TrimSpace(svc.Endpoint) != "" {
		return nil, ErrEndpointBrownfield
	}

	includeAcr := deriveIncludeAcr(root.Services, svc)

	deployments := svc.Deployments
	if deployments == nil {
		deployments = []Deployment{}
	}

	netParams, netMode, err := synthesizeNetwork(svc.Network, in.ServiceName, in.Env, !in.PreserveVarRefs)
	if err != nil {
		return nil, err
	}

	params := map[string]any{
		"deployments": deployments,
		"includeAcr":  includeAcr,
	}
	for k, v := range netParams {
		params[k] = v
	}

	return &Result{
		Parameters:  params,
		NetworkMode: netMode,
	}, nil
}

// BrownfieldDeployments returns the model deployments declared on a brownfield
// (endpoint:) Foundry project service. Synthesize short-circuits with
// ErrEndpointBrownfield before reading deployments:, so the provider uses this
// to learn which model deployments to create on the existing account. Returns
// nil (not an error) when the service declares no deployments.
func BrownfieldDeployments(raw []byte, serviceName string) ([]Deployment, error) {
	if len(raw) == 0 {
		return nil, errors.New("synthesis: raw azure.yaml is empty")
	}
	if serviceName == "" {
		return nil, errors.New("synthesis: serviceName is empty")
	}

	var root projectFile
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse azure.yaml: %w", err)
	}

	node, ok := root.Services[serviceName]
	if !ok {
		return nil, ErrServiceNotFound
	}

	var svc projectService
	if err := node.Decode(&svc); err != nil {
		return nil, fmt.Errorf("decode service %q: %w", serviceName, err)
	}

	return svc.Deployments, nil
}

// resolveServiceRefs expands $ref file includes in one service entry. It decodes
// the node to the map shape foundry.ResolveFileRefs expects, resolves refs
// against projectRoot, and re-encodes to a yaml.Node so the rest of Synthesize
// decodes resolved content instead of raw {"$ref": ...} objects.
func resolveServiceRefs(node yaml.Node, projectRoot, serviceName string) (yaml.Node, error) {
	var raw map[string]any
	if err := node.Decode(&raw); err != nil {
		// Not a mapping (unexpected for a service entry); leave it untouched.
		return node, nil
	}
	resolved, err := foundry.ResolveFileRefs(raw, projectRoot)
	if err != nil {
		return node, fmt.Errorf("resolve $ref includes for service %q: %w", serviceName, err)
	}
	var out yaml.Node
	if err := out.Encode(resolved); err != nil {
		return node, fmt.Errorf("re-encode service %q after $ref resolution: %w", serviceName, err)
	}
	return out, nil
}

// deriveIncludeAcr reports whether provisioning should create an ACR. In the split
// azure.yaml shape, project provisioning reads the azure.ai.project service while
// Docker build settings live on sibling azure.ai.agent services. Until ACR gets a
// first-class project-level switch, any Docker-backed agent in the single-project
// file requires ACR. The legacy inline agents[] scan is kept for hand-authored
// transitional files and tests.
func deriveIncludeAcr(services map[string]yaml.Node, svc projectService) bool {
	for _, a := range svc.Agents {
		if a.Docker != nil && strings.TrimSpace(a.Image) == "" {
			return true
		}
	}

	for _, node := range services {
		var service serviceBlock
		if err := node.Decode(&service); err != nil {
			continue
		}
		if service.Host == "azure.ai.agent" && service.Docker != nil && strings.TrimSpace(service.Image) == "" {
			return true
		}
	}
	return false
}

// Network mode values surfaced for telemetry and emitted as bicep params.
const (
	NetworkModeNone    = "none"
	NetworkModeByo     = "byo"
	NetworkModeManaged = "managed"
)

// Default subnet names used when a subnet descriptor is omitted.
const (
	defaultAgentSubnetName = "agent-subnet"
	defaultPESubnetName    = "pe-subnet"
)

// vnetIDPattern matches a Microsoft.Network/virtualNetworks ARM resource id.
var vnetIDPattern = regexp.MustCompile(
	`(?i)^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Network/virtualNetworks/[^/]+$`,
)

// guidPattern matches a bare GUID.
var guidPattern = regexp.MustCompile(
	`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// rgNamePattern matches a valid Azure resource group name.
var rgNamePattern = regexp.MustCompile(`^[-\w._()]{1,90}$`)

// varRefPattern matches a ${VAR} reference.
var varRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// synthesizeNetwork validates the network: block and returns the bicep
// parameter set plus the telemetry mode. When net is nil the returned
// params disable network isolation and the output is byte-identical to the
// pre-network behavior.
//
// When resolve is true, ${VAR} references in byo.vnet.id / dns.subscription
// are expanded from env (provision path) and an unresolved variable fails.
// When resolve is false (eject path), ${VAR} references are kept verbatim so
// the synthesized parameters file stays environment-portable; the format
// checks that cannot run against an unexpanded placeholder are skipped.
func synthesizeNetwork(
	net *networkBlock,
	svcName string,
	env map[string]string,
	resolve bool,
) (map[string]any, string, error) {
	// Public account: every network param defaults off.
	params := map[string]any{
		"enableNetworkIsolation": false,
		"useManagedEgress":       false,
		"vnetId":                 "",
		"agentSubnetName":        defaultAgentSubnetName,
		"agentSubnetPrefix":      "",
		"createAgentSubnet":      false,
		"peSubnetName":           defaultPESubnetName,
		"peSubnetPrefix":         "",
		"createPESubnet":         false,
		"managedIsolationMode":   "",
		"dnsZonesResourceGroup":  "",
		"dnsZonesSubscription":   "",
	}
	if net == nil {
		return params, NetworkModeNone, nil
	}

	fp := func(suffix string) string {
		return fmt.Sprintf("services.%s.network%s", svcName, suffix)
	}

	// Ingress: a network-bound account always gets an account private endpoint,
	// so peSubnet is mandatory. There is no public data-plane fallback.
	if net.PESubnet == nil {
		return nil, "", fmt.Errorf("%s: private networking requires peSubnet", fp(""))
	}

	// Egress: agentSubnet present injects the agent into the customer subnet;
	// absent uses the Microsoft-managed network.
	useManagedEgress := net.AgentSubnet == nil

	// isolationMode governs the Microsoft-managed network only.
	isoMode := strings.TrimSpace(net.IsolationMode)
	if isoMode != "" {
		if !useManagedEgress {
			return nil, "", fmt.Errorf(
				"%s.isolationMode: only valid for managed egress (omit agentSubnet)", fp(""))
		}
		if isoMode != "AllowInternetOutbound" && isoMode != "AllowOnlyApprovedOutbound" {
			return nil, "", fmt.Errorf(
				"%s.isolationMode: %q is not one of AllowInternetOutbound, AllowOnlyApprovedOutbound",
				fp(""), isoMode)
		}
	}

	// Ingress subnet (account private endpoint).
	peVnet, peName, pePrefix, createPE, err := resolveSubnet(net.PESubnet, fp(".peSubnet"), env, resolve)
	if err != nil {
		return nil, "", err
	}
	vnetID := peVnet

	// Egress subnet (byo only). v1 keeps both subnets in one VNet so a single
	// vnetId drives injection, the PE, and DNS linking.
	if !useManagedEgress {
		agentVnet, agentName, agentPrefix, createAgent, aerr := resolveSubnet(
			net.AgentSubnet, fp(".agentSubnet"), env, resolve)
		if aerr != nil {
			return nil, "", aerr
		}
		if !sameVNet(agentVnet, peVnet) {
			return nil, "", fmt.Errorf(
				"%s: agentSubnet.vnet and peSubnet.vnet must reference the same virtual network", fp(""))
		}
		// The agent and PE subnets share one VNet, so their names must differ.
		// Identical names would point the account private endpoint at the
		// Microsoft.App/environments-delegated agent subnet (PEs cannot live in a
		// delegated subnet), surfacing as a confusing deploy-time failure.
		if strings.EqualFold(agentName, peName) {
			return nil, "", fmt.Errorf(
				"%s: agentSubnet.name and peSubnet.name must differ (both subnets share one VNet)", fp(""))
		}
		params["agentSubnetName"] = agentName
		params["agentSubnetPrefix"] = agentPrefix
		params["createAgentSubnet"] = createAgent
		vnetID = agentVnet
	}

	params["enableNetworkIsolation"] = true
	params["useManagedEgress"] = useManagedEgress
	params["vnetId"] = vnetID
	params["peSubnetName"] = peName
	params["peSubnetPrefix"] = pePrefix
	params["createPESubnet"] = createPE
	params["managedIsolationMode"] = isoMode

	if net.DNS != nil {
		if rg := strings.TrimSpace(net.DNS.ResourceGroup); rg != "" {
			if !rgNamePattern.MatchString(rg) {
				return nil, "", fmt.Errorf("%s.dns.resourceGroup: %q is not a valid resource group name", fp(""), rg)
			}
			params["dnsZonesResourceGroup"] = rg
		}
		if sub := strings.TrimSpace(net.DNS.Subscription); sub != "" {
			if resolve {
				resolved, err := resolveVars(sub, env)
				if err != nil {
					return nil, "", fmt.Errorf("%s.dns.subscription: %w", fp(""), err)
				}
				sub = resolved
			}
			// Normalize to a bare GUID only when concrete; an unexpanded ${VAR}
			// (eject path) is normalized at provision time.
			if containsVarRef(sub) {
				params["dnsZonesSubscription"] = sub
			} else {
				guid, err := normalizeSubscription(sub)
				if err != nil {
					return nil, "", fmt.Errorf("%s.dns.subscription: %w", fp(""), err)
				}
				params["dnsZonesSubscription"] = guid
			}
		}
	}

	mode := NetworkModeByo
	if useManagedEgress {
		mode = NetworkModeManaged
	}
	return params, mode, nil
}

// resolveSubnet validates a subnet descriptor and returns the VNet id, subnet
// name, prefix, and whether azd should create the subnet.
//
//	vnet + name           -> reference existing subnet (create=false)
//	vnet + name + prefix  -> create subnet with that CIDR (create=true)
//
// vnet and name are required; ${VAR} references in vnet are expanded when
// resolve is true and validated as a Microsoft.Network/virtualNetworks id only
// when fully concrete.
func resolveSubnet(
	s *subnetSpec, fieldPath string, env map[string]string, resolve bool,
) (vnetID, name, prefix string, create bool, err error) {
	if s == nil {
		return "", "", "", false, fmt.Errorf("%s: required", fieldPath)
	}
	vnetID = strings.TrimSpace(s.VNet)
	name = strings.TrimSpace(s.Name)
	prefix = strings.TrimSpace(s.Prefix)

	if vnetID == "" {
		return "", "", "", false, fmt.Errorf("%s.vnet: required", fieldPath)
	}
	if name == "" {
		return "", "", "", false, fmt.Errorf("%s.name: required", fieldPath)
	}
	if resolve {
		resolved, rerr := resolveVars(vnetID, env)
		if rerr != nil {
			return "", "", "", false, fmt.Errorf("%s.vnet: %w", fieldPath, rerr)
		}
		vnetID = resolved
	}
	// Validate the ARM id shape only when fully concrete; an unexpanded ${VAR}
	// (eject path) is validated at provision time.
	if !containsVarRef(vnetID) && !vnetIDPattern.MatchString(vnetID) {
		return "", "", "", false, fmt.Errorf(
			"%s.vnet: %q is not a well-formed Microsoft.Network/virtualNetworks id", fieldPath, vnetID)
	}
	if prefix != "" {
		if _, _, perr := net.ParseCIDR(prefix); perr != nil {
			return "", "", "", false, fmt.Errorf("%s.prefix: %q is not a valid CIDR", fieldPath, prefix)
		}
		create = true
	}
	return vnetID, name, prefix, create, nil
}

// sameVNet reports whether two VNet references point at the same VNet. Concrete
// ids compare case-insensitively (ARM ids are case-insensitive); unresolved
// ${VAR} references compare verbatim.
func sameVNet(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if containsVarRef(a) || containsVarRef(b) {
		return a == b
	}
	return strings.EqualFold(a, b)
}

// containsVarRef reports whether s still contains a ${VAR} reference.
func containsVarRef(s string) bool {
	return varRefPattern.MatchString(s)
}

// resolveVars expands ${VAR} references in s using env first, then the
// process environment. An unresolved reference is an error naming the
// variable.
func resolveVars(s string, env map[string]string) (string, error) {
	var unresolved string
	out := varRefPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := varRefPattern.FindStringSubmatch(match)[1]
		if v, ok := env[name]; ok {
			return v
		}
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		if unresolved == "" {
			unresolved = name
		}
		return match
	})
	if unresolved != "" {
		return "", fmt.Errorf("unresolved environment variable ${%s}", unresolved)
	}
	return out, nil
}

// normalizeSubscription accepts a bare GUID or a /subscriptions/<guid>[/...]
// path and returns the bare GUID.
func normalizeSubscription(s string) (string, error) {
	s = strings.TrimSpace(s)
	if guidPattern.MatchString(s) {
		return s, nil
	}
	if strings.HasPrefix(strings.ToLower(s), "/subscriptions/") {
		parts := strings.Split(strings.Trim(s, "/"), "/")
		if len(parts) >= 2 && guidPattern.MatchString(parts[1]) {
			return parts[1], nil
		}
	}
	return "", fmt.Errorf("%q is not a subscription GUID or /subscriptions/<guid> id", s)
}
