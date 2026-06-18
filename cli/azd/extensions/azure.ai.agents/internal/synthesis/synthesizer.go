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
	// ${VAR} references in network fields (byo.vnet.id, dns.subscription).
	// When a referenced variable is absent here, the synthesizer falls back
	// to the process environment before failing. May be nil.
	Env map[string]string

	// PreserveVarRefs keeps ${VAR} references verbatim instead of resolving
	// them. Used by the eject path, where the synthesized main.parameters.json
	// must stay environment-portable: the on-disk provision flow resolves
	// ${VAR} from the azd environment at provision time. When false (the
	// provision path), ${VAR} is resolved here and a missing variable fails.
	PreserveVarRefs bool
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

// dockerBlock is the subset of an agent's docker: object we read to
// decide whether a registry is needed.
type dockerBlock struct {
	Path string `yaml:"path"`
}

// agentBlock is the subset of an agent entry we inspect.
type agentBlock struct {
	Name   string       `yaml:"name"`
	Docker *dockerBlock `yaml:"docker,omitempty"`
	Image  string       `yaml:"image,omitempty"`
}

// foundryService is the subset of a services.<name> body the synthesizer
// reads. Unknown fields (connections, tools, agents[].tools, etc.) are
// intentionally ignored: they are reconciled in azd deploy, not provision.
type foundryService struct {
	Host        string        `yaml:"host"`
	Endpoint    string        `yaml:"endpoint,omitempty"`
	Deployments []Deployment  `yaml:"deployments,omitempty"`
	Agents      []agentBlock  `yaml:"agents,omitempty"`
	Network     *networkBlock `yaml:"network,omitempty"`
}

// networkBlock mirrors the network: sub-tree on the service body.
type networkBlock struct {
	Mode    string        `yaml:"mode"`
	Byo     *byoBlock     `yaml:"byo,omitempty"`
	Managed *managedBlock `yaml:"managed,omitempty"`
	DNS     *dnsBlock     `yaml:"dns,omitempty"`
}

// byoBlock mirrors network.byo (bring-your-own VNet).
type byoBlock struct {
	VNet        *vnetRef    `yaml:"vnet,omitempty"`
	AgentSubnet *subnetSpec `yaml:"agentSubnet,omitempty"`
	PESubnet    *subnetSpec `yaml:"peSubnet,omitempty"`
}

// vnetRef mirrors network.byo.vnet.
type vnetRef struct {
	ID string `yaml:"id"`
}

// subnetSpec mirrors a tri-state subnet descriptor.
type subnetSpec struct {
	Name   string `yaml:"name,omitempty"`
	Prefix string `yaml:"prefix,omitempty"`
}

// managedBlock mirrors network.managed (Foundry-managed VNet).
type managedBlock struct {
	IsolationMode string `yaml:"isolationMode,omitempty"`
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
// Foundry service in azure.yaml.
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

	var svc foundryService
	if err := node.Decode(&svc); err != nil {
		return nil, fmt.Errorf("decode service %q: %w", in.ServiceName, err)
	}

	if len(in.AcceptedHosts) > 0 && !slices.Contains(in.AcceptedHosts, svc.Host) {
		return nil, ErrServiceNotFound
	}
	if svc.Endpoint != "" {
		return nil, ErrEndpointBrownfield
	}

	includeAcr := false
	for _, a := range svc.Agents {
		if a.Docker != nil {
			includeAcr = true
			break
		}
	}

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
		"networkMode":            "",
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

	// Mode coherence.
	switch net.Mode {
	case NetworkModeByo:
		if net.Byo == nil {
			return nil, "", fmt.Errorf("%s: mode is byo but byo: block is missing", fp(""))
		}
		if net.Managed != nil {
			return nil, "", fmt.Errorf("%s: mode is byo but managed: block is also set", fp(""))
		}
	case NetworkModeManaged:
		if net.Managed == nil {
			return nil, "", fmt.Errorf("%s: mode is managed but managed: block is missing", fp(""))
		}
		if net.Byo != nil {
			return nil, "", fmt.Errorf("%s: mode is managed but byo: block is also set", fp(""))
		}
	case "":
		return nil, "", fmt.Errorf("%s: mode is required when network: is present", fp(""))
	default:
		return nil, "", fmt.Errorf("%s.mode: %q is not one of byo, managed", fp(""), net.Mode)
	}

	params["enableNetworkIsolation"] = true
	params["networkMode"] = net.Mode

	if net.Mode == NetworkModeByo {
		if net.Byo.VNet == nil || strings.TrimSpace(net.Byo.VNet.ID) == "" {
			return nil, "", fmt.Errorf("%s.byo.vnet.id: required in v1", fp(""))
		}
		vnetID := strings.TrimSpace(net.Byo.VNet.ID)
		if resolve {
			resolved, err := resolveVars(vnetID, env)
			if err != nil {
				return nil, "", fmt.Errorf("%s.byo.vnet.id: %w", fp(""), err)
			}
			vnetID = resolved
		}
		// Validate the ARM id shape only when the value is fully concrete; an
		// unexpanded ${VAR} (eject path) is validated at provision time.
		if !containsVarRef(vnetID) && !vnetIDPattern.MatchString(vnetID) {
			return nil, "", fmt.Errorf(
				"%s.byo.vnet.id: %q is not a well-formed Microsoft.Network/virtualNetworks id",
				fp(""), vnetID)
		}
		params["vnetId"] = vnetID

		agentName, agentPrefix, createAgent, err := resolveSubnet(
			net.Byo.AgentSubnet, defaultAgentSubnetName, fp(".byo.agentSubnet"))
		if err != nil {
			return nil, "", err
		}
		params["agentSubnetName"] = agentName
		params["agentSubnetPrefix"] = agentPrefix
		params["createAgentSubnet"] = createAgent

		peName, pePrefix, createPE, err := resolveSubnet(
			net.Byo.PESubnet, defaultPESubnetName, fp(".byo.peSubnet"))
		if err != nil {
			return nil, "", err
		}
		params["peSubnetName"] = peName
		params["peSubnetPrefix"] = pePrefix
		params["createPESubnet"] = createPE
	}

	if net.Mode == NetworkModeManaged {
		mode := net.Managed.IsolationMode
		if mode != "" &&
			mode != "AllowInternetOutbound" &&
			mode != "AllowOnlyApprovedOutbound" {
			return nil, "", fmt.Errorf(
				"%s.managed.isolationMode: %q is not one of AllowInternetOutbound, AllowOnlyApprovedOutbound",
				fp(""), mode)
		}
		params["managedIsolationMode"] = mode
	}

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

	return params, net.Mode, nil
}

// resolveSubnet applies the subnet tri-state rules and returns the resolved
// name, prefix, and whether azd should create the subnet.
//
//	nil          -> create default subnet (defaultName, default prefix, create=true)
//	name only    -> reference existing subnet (name, "", create=false)
//	name+prefix  -> create subnet (name, prefix, create=true)
//	prefix only  -> error
func resolveSubnet(s *subnetSpec, defaultName, fieldPath string) (string, string, bool, error) {
	if s == nil {
		return defaultName, "", true, nil
	}
	name := strings.TrimSpace(s.Name)
	prefix := strings.TrimSpace(s.Prefix)

	if prefix != "" && name == "" {
		return "", "", false, fmt.Errorf("%s: prefix set without name", fieldPath)
	}
	if prefix != "" {
		if _, _, err := net.ParseCIDR(prefix); err != nil {
			return "", "", false, fmt.Errorf("%s: %q is not a valid CIDR", fieldPath, prefix)
		}
		return name, prefix, true, nil
	}
	if name != "" {
		// Reference an existing subnet.
		return name, "", false, nil
	}
	// Empty descriptor: treat like omitted.
	return defaultName, "", true, nil
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
