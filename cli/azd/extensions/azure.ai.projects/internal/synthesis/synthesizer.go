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
	"maps"
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

	// Env maps project-wide azd values used by network fields and conditions.
	// Missing values may fall back to the process environment.
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

// codeConfigBlock marks an agent as a code (ZIP) deploy. Its presence is the
// signal; the keys are camelCase because the unified azure.ai.agent service
// entry is serialized from the agent definition's JSON tags.
type codeConfigBlock struct {
	Runtime    string `yaml:"runtime,omitempty"`
	EntryPoint string `yaml:"entryPoint,omitempty"`
}

// agentBlock is the subset of an agent entry we inspect — both the legacy inline
// agents[] shape and the unified azure.ai.agent service shape.
type agentBlock struct {
	Name              string           `yaml:"name,omitempty"`
	Kind              string           `yaml:"kind,omitempty"`
	Image             string           `yaml:"image,omitempty"`
	CodeConfiguration *codeConfigBlock `yaml:"codeConfiguration,omitempty"`
}

// serviceBlock is the subset of a service entry we inspect for cross-service provisioning inputs.
type serviceBlock struct {
	Host              string           `yaml:"host"`
	Kind              string           `yaml:"kind,omitempty"`
	Image             string           `yaml:"image,omitempty"`
	CodeConfiguration *codeConfigBlock `yaml:"codeConfiguration,omitempty"`
	Config            *agentBlock      `yaml:"config,omitempty"`
	Agents            []agentBlock     `yaml:"agents,omitempty"`
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

	svc, err := loadProjectService(root.Services, in.ServiceName, in.ProjectRoot)
	if err != nil {
		return nil, err
	}

	if len(in.AcceptedHosts) > 0 && !slices.Contains(in.AcceptedHosts, svc.Host) {
		return nil, ErrServiceNotFound
	}
	endpoint, err := expandEndpoint(svc.Endpoint, in.Env)
	if err != nil {
		return nil, err
	}
	if endpoint != "" {
		return nil, ErrEndpointBrownfield
	}

	includeAcr, err := deriveIncludeAcr(
		root.Services,
		svc,
		in.ProjectRoot,
		in.Env,
	)
	if err != nil {
		return nil, err
	}

	deployments := svc.Deployments
	if deployments == nil {
		deployments = []Deployment{}
	}

	netParams, netMode, err := synthesizeNetwork(svc.Network, in.ServiceName, in.Env, !in.PreserveVarRefs)
	if err != nil {
		return nil, err
	}
	if includeAcr && netMode != NetworkModeNone {
		return nil, errors.New(
			"synthesis: private networking does not support an auto-created Azure Container Registry; specify an image instead",
		)
	}

	params := map[string]any{
		"deployments": deployments,
		"includeAcr":  includeAcr,
	}
	maps.Copy(params, netParams)

	return &Result{
		Parameters:  params,
		NetworkMode: netMode,
	}, nil
}

// SynthesizeExistingProject derives parameters for editable infrastructure that
// augments an existing Foundry project without taking ownership of it.
func SynthesizeExistingProject(in Input) (*Result, error) {
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
	svc, err := loadProjectService(root.Services, in.ServiceName, in.ProjectRoot)
	if err != nil {
		return nil, err
	}
	if len(in.AcceptedHosts) > 0 && !slices.Contains(in.AcceptedHosts, svc.Host) {
		return nil, ErrServiceNotFound
	}
	if strings.TrimSpace(svc.Endpoint) == "" {
		return nil, errors.New("synthesis: existing Foundry project endpoint is empty")
	}

	includeAcr, err := deriveIncludeAcr(
		root.Services,
		svc,
		in.ProjectRoot,
		in.Env,
	)
	if err != nil {
		return nil, err
	}
	deployments := svc.Deployments
	if deployments == nil {
		deployments = []Deployment{}
	}

	return &Result{Parameters: map[string]any{
		"deployments": deployments,
		"includeAcr":  includeAcr,
	}, NetworkMode: NetworkModeNone}, nil
}

// BrownfieldDeployments returns the model deployments declared on a brownfield
// (endpoint:) Foundry project service. Synthesize short-circuits with
// ErrEndpointBrownfield before reading deployments:, so the provider uses this
// to learn which model deployments to create on the existing account. Returns
// nil (not an error) when the service declares no deployments.
func BrownfieldDeployments(
	raw []byte,
	serviceName string,
	projectRoot string,
) ([]Deployment, error) {
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

	svc, err := loadProjectService(root.Services, serviceName, projectRoot)
	if err != nil {
		return nil, err
	}

	return svc.Deployments, nil
}

// ProjectEndpoint returns the endpoint configured on a Foundry project service,
// with ${VAR} references resolved from env. It resolves $ref includes before
// decoding the service body.
func ProjectEndpoint(
	raw []byte,
	serviceName string,
	projectRoot string,
	env map[string]string,
) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("synthesis: raw azure.yaml is empty")
	}

	var root projectFile
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return "", fmt.Errorf("parse azure.yaml: %w", err)
	}

	svc, err := loadProjectService(root.Services, serviceName, projectRoot)
	if err != nil {
		return "", err
	}
	return expandEndpoint(svc.Endpoint, env)
}

func expandEndpoint(raw string, env map[string]string) (string, error) {
	mapping := func(name string) string {
		if value, found := env[name]; found {
			return value
		}
		value, _ := os.LookupEnv(name)
		return value
	}
	expanded, err := foundry.ExpandEnv(strings.TrimSpace(raw), mapping)
	if err != nil {
		return "", fmt.Errorf("expand endpoint: %w", err)
	}
	return strings.TrimSpace(expanded), nil
}

// loadProjectService decodes a service after resolving any local $ref includes.
func loadProjectService(
	services map[string]yaml.Node,
	serviceName string,
	projectRoot string,
) (projectService, error) {
	node, ok := services[serviceName]
	if !ok {
		return projectService{}, ErrServiceNotFound
	}
	if err := rejectBundledDeclarations(node, serviceName, projectRoot != ""); err != nil {
		return projectService{}, err
	}
	if projectRoot != "" {
		var err error
		node, err = resolveServiceRefs(node, projectRoot, serviceName)
		if err != nil {
			return projectService{}, err
		}
	}
	if err := rejectBundledDeclarations(node, serviceName, false); err != nil {
		return projectService{}, err
	}

	var svc projectService
	if err := node.Decode(&svc); err != nil {
		return projectService{}, fmt.Errorf("decode service %q: %w", serviceName, err)
	}
	return svc, nil
}

// rejectBundledDeclarations rejects legacy resource payloads, not agent references
// to independently owned resources. Before expanding refs, string connection names
// may defer the kind check to a referenced definition. Always recheck after resolution.
func rejectBundledDeclarations(node yaml.Node, path string, resolvingRefs bool) error {
	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return nil
	}
	kind, hasKind := fields["kind"]
	prompt := kind.Kind == yaml.ScalarNode && kind.Tag == "!!str" &&
		strings.EqualFold(strings.TrimSpace(kind.Value), "prompt")
	ref := fields["$ref"]
	deferredKind := resolvingRefs && !hasKind && ref.Kind == yaml.ScalarNode &&
		ref.Tag == "!!str" && strings.TrimSpace(ref.Value) != ""
	for _, field := range []string{"connections", "toolboxes"} {
		value, ok := fields[field]
		if !ok {
			continue
		}
		if field == "connections" && (prompt || deferredKind) && value.Kind == yaml.SequenceNode {
			if !slices.ContainsFunc(value.Content, func(item *yaml.Node) bool {
				return item.Kind != yaml.ScalarNode || item.Tag != "!!str" || strings.TrimSpace(item.Value) == ""
			}) {
				continue
			}
		}
		if field == "toolboxes" && value.Kind == yaml.SequenceNode {
			if !slices.ContainsFunc(value.Content, func(item *yaml.Node) bool {
				if item.Kind == yaml.ScalarNode && item.Tag == "!!str" && strings.TrimSpace(item.Value) != "" {
					return false
				}
				var reference map[string]yaml.Node
				if err := item.Decode(&reference); err != nil || len(reference) != 1 {
					return true
				}
				// A reference-only item is validated again after file expansion.
				if ref := reference["$ref"]; ref.Kind == yaml.ScalarNode && ref.Tag == "!!str" {
					return false
				}
				name := reference["name"]
				return name.Kind != yaml.ScalarNode || name.Tag != "!!str" || strings.TrimSpace(name.Value) == ""
			}) {
				continue
			}
		}
		return fmt.Errorf("services.%s.%s: bundled declarations are no longer supported; "+
			"migrate them to independent azure.ai.connection or azure.ai.toolbox services", path, field)
	}
	if config, ok := fields["config"]; ok {
		if err := rejectBundledDeclarations(config, path+".config", resolvingRefs); err != nil {
			return err
		}
	}
	if agents, ok := fields["agents"]; ok && agents.Kind == yaml.SequenceNode {
		for i, agent := range agents.Content {
			if err := rejectBundledDeclarations(*agent, fmt.Sprintf("%s.agents[%d]", path, i), resolvingRefs); err != nil {
				return err
			}
		}
	}
	return nil
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

func serviceForHost(
	node yaml.Node,
	projectRoot string,
	serviceName string,
	expectedHost string,
) (yaml.Node, bool, error) {
	var selector struct {
		Host string `yaml:"host"`
		Ref  string `yaml:"$ref"`
	}
	if err := node.Decode(&selector); err != nil {
		return node, false, nil
	}
	if selector.Host != "" && selector.Host != expectedHost {
		return node, false, nil
	}
	if selector.Host == "" && selector.Ref == "" {
		return node, false, nil
	}
	if expectedHost == "azure.ai.agent" && projectRoot != "" {
		if err := validateAgentRootRefCoreFields(
			node,
			projectRoot,
			serviceName,
		); err != nil {
			return node, false, err
		}
	}
	if projectRoot != "" {
		resolved, err := resolveServiceRefs(
			node,
			projectRoot,
			serviceName,
		)
		if err != nil {
			return node, false, err
		}
		node = resolved
	}
	if err := node.Decode(&selector); err != nil {
		return node, false, fmt.Errorf(
			"decode service %q selector: %w",
			serviceName,
			err,
		)
	}
	return node, selector.Host == expectedHost, nil
}

func validateAgentRootRefCoreFields(
	node yaml.Node,
	projectRoot string,
	serviceName string,
) error {
	var raw map[string]any
	if err := node.Decode(&raw); err != nil {
		return nil
	}
	ref, _ := raw["$ref"].(string)
	if ref == "" {
		return nil
	}
	referenced, err := foundry.ResolveFileRefs(
		map[string]any{"$ref": ref},
		projectRoot,
	)
	if err != nil {
		return fmt.Errorf(
			"resolve $ref includes for service %q: %w",
			serviceName,
			err,
		)
	}
	host, _ := raw["host"].(string)
	if host == "" {
		host, _ = referenced["host"].(string)
	}
	if host != "azure.ai.agent" {
		return nil
	}
	for _, field := range []string{
		"project",
		"language",
		"image",
		"docker",
	} {
		if _, found := referenced[field]; found {
			return fmt.Errorf(
				"service %q root $ref must not provide core field %q; declare it in azure.yaml",
				serviceName,
				field,
			)
		}
	}
	return nil
}

// deriveIncludeAcr reports whether provisioning should create an ACR. An ACR is
// needed when any agent is a hosted, build-from-source agent: azd builds its
// image and pushes it to the registry. Agents live on sibling azure.ai.agent
// services in the split shape, or under agents[] in the legacy inline shape;
// both are scanned. The decision mirrors the deploy-time contract:
//
//   - codeConfiguration present -> code (ZIP) deploy, no ACR
//   - image present             -> pre-built image (BYO registry), no ACR
//   - otherwise (hosted)        -> container build from source, needs ACR
//
// Keying on image/codeConfiguration rather than the optional docker: block is
// deliberate: docker: is not in the agent schema and is dropped by omitempty
// when remoteBuild is false, so it is not a reliable build signal.
func deriveIncludeAcr(
	services map[string]yaml.Node,
	svc projectService,
	projectRoot string,
	env map[string]string,
) (bool, error) {
	includeAcr := slices.ContainsFunc(svc.Agents, agentNeedsAcr)
	lookup := projectConditionLookup(env)
	for _, serviceName := range slices.Sorted(maps.Keys(services)) {
		node := services[serviceName]
		var selector struct {
			Host string `yaml:"host"`
			Ref  string `yaml:"$ref"`
		}
		if err := node.Decode(&selector); err != nil {
			continue
		}
		// Other service targets own their payloads, refs and conditions.
		if selector.Host != "azure.ai.agent" && (selector.Host != "" || selector.Ref == "") {
			continue
		}
		enabled, err := serviceNodeEnabled(node, lookup)
		if err != nil {
			return false, fmt.Errorf("services.%s.condition: %w", serviceName, err)
		}
		if !enabled {
			continue
		}
		if err := rejectBundledDeclarations(node, serviceName, projectRoot != ""); err != nil {
			return false, err
		}
		node, matches, err := serviceForHost(node, projectRoot, serviceName, "azure.ai.agent")
		if err != nil {
			return false, err
		}
		if !matches {
			continue
		}
		if err := rejectBundledDeclarations(node, serviceName, false); err != nil {
			return false, err
		}
		var service serviceBlock
		if err := node.Decode(&service); err != nil {
			return false, fmt.Errorf("decode service %q: %w", serviceName, err)
		}
		agent := agentBlock{
			Kind:              service.Kind,
			Image:             service.Image,
			CodeConfiguration: service.CodeConfiguration,
		}
		if service.Config != nil {
			if agent.Kind == "" {
				agent.Kind = service.Config.Kind
			}
			if agent.Image == "" {
				agent.Image = service.Config.Image
			}
			if agent.CodeConfiguration == nil {
				agent.CodeConfiguration = service.Config.CodeConfiguration
			}
		}
		includeAcr = includeAcr || agentNeedsAcr(agent)
	}
	return includeAcr, nil
}

// agentNeedsAcr reports whether a single agent entry builds a container image
// from source (and therefore requires an ACR). Pre-built images and code/ZIP
// deploys do not; any other hosted agent does.
func agentNeedsAcr(a agentBlock) bool {
	if a.CodeConfiguration != nil || strings.TrimSpace(a.Image) != "" {
		return false
	}
	// "hosted" is the only container kind; an empty kind defaults to hosted for
	// back-compat. Other explicit kinds (prompt, prompt-voice, workflow) do not
	// build a container image.
	// NOTE: if a future non-container kind can omit kind:, replace this
	// default-to-hosted with an explicit allowlist so it does not trigger ACR.
	kind := strings.TrimSpace(a.Kind)
	return kind == "" || strings.EqualFold(kind, "hosted")
}

func serviceNodeEnabled(
	node yaml.Node,
	lookup func(string) string,
) (bool, error) {
	value, present, err := serviceConditionValue(node)
	if err != nil {
		return false, err
	}
	if !present {
		return true, nil
	}
	return evaluateCondition(value, lookup)
}

func serviceConditionValue(node yaml.Node) (string, bool, error) {
	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return "", false, nil
	}
	cond, ok := fields["condition"]
	if !ok {
		return "", false, nil
	}
	if cond.Kind != yaml.ScalarNode {
		return "", true, fmt.Errorf("condition must be a scalar")
	}
	if cond.Tag == "!!null" {
		return "", true, nil
	}
	return cond.Value, true, nil
}

func projectConditionLookup(env map[string]string) func(string) string {
	return func(name string) string {
		if value, found := env[name]; found {
			return value
		}
		value, _ := os.LookupEnv(name)
		return value
	}
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
			// Rejected before either path reads it: an unsupported '$' form is
			// silently rewritten by the expander on the provision path, and
			// written verbatim into the ejected template on the other.
			if err := ValidateEnvReferences(sub); err != nil {
				return nil, "", fmt.Errorf("%s.dns.subscription: %w", fp(""), err)
			}
			if resolve {
				resolved, err := resolveVars(sub, env)
				if err != nil {
					return nil, "", fmt.Errorf("%s.dns.subscription: %w", fp(""), err)
				}
				sub = resolved
			}
			// Normalize to a bare GUID only when the value is final. On the eject
			// path an unexpanded ${VAR} is normalized at provision time; once
			// resolveVars has run there is nothing left to expand, so anything
			// still shaped like a reference (an escaped $${VAR} resolves to a
			// literal ${VAR}) is a subscription id that never will be.
			if !resolve && containsVarRef(sub) {
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
// resolve is true. The Microsoft.Network/virtualNetworks id shape is then
// checked, except on the eject path (resolve false), where an unexpanded
// reference is left for provision time to validate.
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
	// Rejected on both paths: see the dns.subscription call for why this cannot
	// wait for resolveVars.
	if err := ValidateEnvReferences(vnetID); err != nil {
		return "", "", "", false, fmt.Errorf("%s.vnet: %w", fieldPath, err)
	}
	if resolve {
		resolved, rerr := resolveVars(vnetID, env)
		if rerr != nil {
			return "", "", "", false, fmt.Errorf("%s.vnet: %w", fieldPath, rerr)
		}
		vnetID = resolved
	}
	// Validate the ARM id shape unless the value can still change: on the eject
	// path an unexpanded ${VAR} is validated at provision time. After
	// resolveVars there is nothing left to expand, so a leftover ${VAR} (what an
	// escaped $${VAR} resolves to) is checked now rather than deferred to a
	// provision that can only fail.
	if (resolve || !containsVarRef(vnetID)) && !vnetIDPattern.MatchString(vnetID) {
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

// containsVarRef reports whether s still carries an azd ${VAR} reference the
// expander will resolve, including the ${VAR:-default} form.
//
// Escaped references and names reserved by a Foundry ${{...}} span do not count:
// [foundry.ExpandEnv] leaves those alone, so the value is already as concrete as
// it will ever be and the caller's own shape validation should run on it.
func containsVarRef(s string) bool {
	return len(FindEnvReferences(s)) > 0
}

// resolveVars expands ${VAR} references in s using env first, then the
// process environment. An unresolved reference is an error naming the
// variable.
//
// Expansion routes through foundry.ExpandEnv, the shared expander every other
// Foundry field uses, so ${VAR:-default} and the $${VAR} escape behave here
// exactly as they do elsewhere.
//
// ExpandEnv resolves through a mapping callback that only receives the variable
// name, so the names that must resolve are collected up front from
// [FindEnvReferences]: a name is required only where it occurs at least once
// without a :- default, in a position the expander will actually act on. Reusing
// that scanner is what keeps an escaped or ${{...}} reserved occurrence from
// making a live, defaulted occurrence of the same name look unresolvable.
//
// Callers validate the value with [ValidateEnvReferences] first, so every
// occurrence the expander acts on is one the scanner saw.
func resolveVars(s string, env map[string]string) (string, error) {
	required := map[string]struct{}{}
	for _, reference := range FindEnvReferences(s) {
		if !reference.HasDefault {
			required[reference.Name] = struct{}{}
		}
	}

	var unresolved string
	out, err := foundry.ExpandEnv(s, func(name string) string {
		if v, ok := env[name]; ok {
			return v
		}
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		if _, ok := required[name]; ok && unresolved == "" {
			unresolved = name
		}
		return ""
	})
	if err != nil {
		return "", err
	}
	if unresolved != "" {
		return "", fmt.Errorf("unresolved environment variable ${%s}", unresolved)
	}
	return out, nil
}

// ResolveEnvironmentValue expands environment references.
func ResolveEnvironmentValue(value string, env map[string]string) (string, error) {
	return resolveVars(value, env)
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
