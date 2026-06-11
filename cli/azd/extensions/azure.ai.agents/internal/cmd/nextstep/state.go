// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	// agentHost matches the `host:` value of a Foundry service entry in
	// azure.yaml under the unified design. Duplicated here (rather than
	// imported from the parent cmd package) so nextstep stays a leaf
	// package — cmd and project both import nextstep, so a reverse import
	// would close a cycle.
	agentHost = "microsoft.foundry"

	// agentVersionVarFormat is the env-var name that signals a deployed
	// agent. Filled with the upper-cased agent key.
	agentVersionVarFormat = "AGENT_%s_VERSION"

	// projectEndpointVar is the env-var that carries the Foundry project
	// endpoint URL produced by `azd ai agent init`.
	projectEndpointVar = "FOUNDRY_PROJECT_ENDPOINT"

	// useExistingAIProjectVar was removed in 4.13. The
	// USE_EXISTING_AI_PROJECT env var is still written by `azd ai
	// agent init` for Bicep's "skip project creation" branch, but
	// the resolver no longer consumes it directly — the
	// equivalent "project not yet provisioned" signal is now
	// expressed via the "project" tag in AI_AGENT_PENDING_PROVISION
	// (see pendingProvisionVar below and pending_provision.go in
	// the cmd package). Single source of truth keeps the producer
	// (init.go) and consumer (this resolver) in lock-step without a
	// second env-var contract to maintain.

	// pendingProvisionVar names the extension-owned env var that
	// lists resource-class tags init configured but provision has
	// not yet materialized. See State.PendingProvisionReasons for
	// the full semantics and pending_provision.go in the cmd package
	// for the read/write helpers and the reason-tag taxonomy. The
	// constant is duplicated here (rather than imported from cmd)
	// because nextstep is a leaf package with no dependency on cmd
	// — both packages share the same string literal contract.
	pendingProvisionVar = "AI_AGENT_PENDING_PROVISION"

	azureSubscriptionIdVar = "AZURE_SUBSCRIPTION_ID"
	azureLocationVar       = "AZURE_LOCATION"
)

// envVarRefPattern captures ${VAR} references inside string values.
// Group 1 is the variable name. Group 2 captures the optional default
// tail `:-fallback`; when group 2 is non-empty the author explicitly
// opted into a fallback and the variable is therefore not required at
// deploy time (the runtime expander `drone/envsubst` honors `:-`
// semantics). `extractEnvRefs` skips refs with a non-empty group 2 so
// they never surface in the missing-vars hints; the variable is reported
// as missing only when authored as the bare `${VAR}` form. Variable names
// follow the standard shell convention: leading letter or underscore,
// then alphanumeric or underscore.
//
// Note the leading `\$\{` does not match the `${{...}}` Foundry
// server-side form (the second `{` is not a valid variable-name start),
// so those references are intentionally left alone.
var envVarRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-[^}]*)?\}`)

// Source is the read-only view of azd that AssembleState needs.
//
// The production implementation wraps an *azdext.AzdClient via NewSource;
// tests inject a fake. The split keeps the package free of gRPC plumbing.
type Source interface {
	// Project returns the parsed azure.yaml of the current project, or an
	// error if no project is present.
	Project(ctx context.Context) (*azdext.ProjectConfig, error)
	// CurrentEnvName returns the name of the active azd environment.
	CurrentEnvName(ctx context.Context) (string, error)
	// EnvValue returns the value of key in the named environment. An empty
	// string with a nil error means the key is unset; transport errors are
	// surfaced verbatim.
	EnvValue(ctx context.Context, envName, key string) (string, error)
}

// NewSource adapts an *azdext.AzdClient to the Source interface. The
// returned Source borrows the client; the caller retains ownership and
// is responsible for closing it.
func NewSource(client *azdext.AzdClient) Source {
	return &clientSource{client: client}
}

type clientSource struct {
	client *azdext.AzdClient
}

func (s *clientSource) Project(ctx context.Context) (*azdext.ProjectConfig, error) {
	resp, err := s.client.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Project == nil {
		return nil, errors.New("azd returned an empty project response")
	}
	return resp.Project, nil
}

func (s *clientSource) CurrentEnvName(ctx context.Context) (string, error) {
	resp, err := s.client.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Environment == nil {
		return "", errors.New("azd returned an empty environment response")
	}
	return resp.Environment.Name, nil
}

func (s *clientSource) EnvValue(ctx context.Context, envName, key string) (string, error) {
	resp, err := s.client.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: envName,
		Key:     key,
	})
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", nil
	}
	return resp.Value, nil
}

// Option configures AssembleState.
type Option func(*config)

type config struct {
	// openAPIAgent and openAPISuffix together enable a cache-only OpenAPI
	// payload lookup. The zero value (empty strings) disables the probe.
	openAPIAgent  string
	openAPISuffix string

	// openAPILiveFetch, when non-nil, is consulted before the on-disk
	// cache: a non-empty body wins and is used for example extraction.
	// On error or empty body the assembler silently falls back to the
	// cache lookup configured via WithOpenAPIProbe. Used by
	// `azd ai agent run` to surface a fresh sample without making the
	// on-disk cache the source of truth.
	openAPILiveFetch func(context.Context) ([]byte, error)

	// createdFolderDisplay is a pre-computed relative display path for
	// the folder created during init (e.g., "my-agent"). Empty when
	// init did not create a new directory.
	createdFolderDisplay string
}

// WithOpenAPIProbe enables a cache-only OpenAPI lookup for (agentName, suffix).
// Empty inputs disable the probe; misses or malformed specs leave HasOpenAPI
// false. Combine with WithLiveOpenAPIProbe to prefer a fresh in-process fetch.
func WithOpenAPIProbe(agentName, suffix string) Option {
	return func(c *config) {
		c.openAPIAgent = agentName
		c.openAPISuffix = suffix
	}
}

// WithLiveOpenAPIProbe enables an HTTP fetch of the agent's OpenAPI
// spec. When the supplied closure returns a non-empty byte slice with a
// nil error, those bytes are used for example extraction in preference
// to the on-disk cache; any error or empty body falls back to the
// cache lookup configured via WithOpenAPIProbe.
//
// The caller owns the probe's timeout — pass a closure that wraps the
// HTTP call in its own short-lived context (the design budget is 3 s
// for `azd ai agent run`). The probe is intended for transient "just
// started" scenarios where the live spec is authoritative; cache-only
// paths (show / deploy) should not register a live probe.
func WithLiveOpenAPIProbe(fetch func(context.Context) ([]byte, error)) Option {
	return func(c *config) { c.openAPILiveFetch = fetch }
}

// WithCreatedFolder passes a pre-computed display path for the folder
// created during init (e.g., "my-agent"). The resolver prepends a
// `cd <folder>` suggestion when this is non-empty. The caller is
// responsible for computing the relative/slash-normalized path.
func WithCreatedFolder(displayPath string) Option {
	return func(c *config) { c.createdFolderDisplay = displayPath }
}

// AssembleState builds a State snapshot for the current azd environment.
//
// All probes are best-effort: transport or parse errors are collected
// and returned alongside a partially-populated state, so the resolver
// can still degrade gracefully (e.g., suggest `azd init` when project
// load fails). Callers should render guidance from the returned State
// even when len(errs) > 0.
func AssembleState(
	ctx context.Context,
	client *azdext.AzdClient,
	opts ...Option,
) (*State, []error) {
	return assembleState(ctx, NewSource(client), opts...)
}

// AssembleStateFromSource is the Source-injecting variant of AssembleState.
// Production reaches this via show.go's `resolveNextStepFromSource`, which
// constructs a Source explicitly so it can later be swapped for a fake in
// tests. Use AssembleState directly when constructing from a real
// *azdext.AzdClient; use this when you already have a Source (production
// or test fake).
func AssembleStateFromSource(
	ctx context.Context,
	src Source,
	opts ...Option,
) (*State, []error) {
	return assembleState(ctx, src, opts...)
}

func assembleState(ctx context.Context, src Source, opts ...Option) (*State, []error) {
	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}

	state := &State{}
	state.CreatedFolderDisplay = cfg.createdFolderDisplay
	var errs []error

	envName, err := src.CurrentEnvName(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("read current environment: %w", err))
	}

	if envName != "" {
		endpoint, err := src.EnvValue(ctx, envName, projectEndpointVar)
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", projectEndpointVar, err))
		}
		state.HasProjectEndpoint = endpoint != ""

		// PendingProvisionReasons is the generalized "init configured
		// something provision still has to materialize" signal that
		// the model-deployment / ACR / App-Insights blank-input
		// branches write into. Read here so the resolver and doctor
		// share one snapshot. Unknown tags are kept verbatim — the
		// resolver only checks for non-emptiness, and downstream
		// readers may interpret tags they recognize. Transport
		// errors are surfaced into errs but do not abort assembly;
		// the field is best-effort and the resolver tolerates an
		// empty list (it falls back to legacy heuristics in that
		// case).
		pending, err := src.EnvValue(ctx, envName, pendingProvisionVar)
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", pendingProvisionVar, err))
		}
		state.PendingProvisionReasons = parsePendingProvisionReasons(pending)

		state.MissingAzureContextVars = detectMissingAzureContextVars(ctx, src, envName, &errs)
	}

	project, err := src.Project(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("load project: %w", err))
	}

	// collectFoundry decodes each Foundry service entry once and fills
	// both state.Services (one entry per agent) and the aggregate
	// resource refs (ModelRefs / Toolboxes / Connections + Has* flags).
	collectFoundry(ctx, src, envName, project, state, &errs)

	if project != nil && envName != "" {
		state.MissingInfraVars, state.MissingManualVars = detectMissingVars(
			ctx, src, envName, project.Path, state.Services, &errs,
		)
		populateOpenAPIPayload(ctx, cfg, project.Path, envName, state)
	}

	// Partition toolbox-derived endpoint vars out of MissingManualVars
	// into MissingToolboxEndpoints. This must run AFTER collectFoundry
	// because it depends on state.Toolboxes being populated — without the
	// toolbox list we cannot tell a toolbox-derived var
	// ("TOOLBOX_WEB_SEARCH_TOOLS_MCP_ENDPOINT" for a declared
	// `web-search-tools` toolbox) apart from a generic user-named variable
	// that happens to start with TOOLBOX_. See MissingToolboxEndpoints
	// docs (types.go) for the rationale.
	partitionToolboxEndpointVars(state)

	return state, errs
}

func detectMissingAzureContextVars(ctx context.Context, src Source, envName string, errs *[]error) []string {
	requiredVars := []string{azureSubscriptionIdVar, azureLocationVar}
	missing := make([]string, 0, len(requiredVars))
	for _, key := range requiredVars {
		value, err := src.EnvValue(ctx, envName, key)
		if err != nil {
			*errs = append(*errs, fmt.Errorf("read %s: %w", key, err))
			continue
		}
		if strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}

	return missing
}

// partitionToolboxEndpointVars moves any entry in state.MissingManualVars
// whose name is the canonical TOOLBOX_<NAME>_MCP_ENDPOINT key for a
// manifest-declared toolbox into state.MissingToolboxEndpoints. The
// partition is a no-op when state.Toolboxes is empty: any TOOLBOX_*
// entry in MissingManualVars without a corresponding manifest toolbox
// is a generic user variable and stays where it is.
//
// state.MissingManualVars order is preserved (caller-visible sorting
// happens in the resolver). The matched ResourceRefs are then sorted
// by (Name, ServiceName) before being written to MissingToolboxEndpoints
// so callers see a stable ordering that matches state.Toolboxes regardless
// of how MissingManualVars happens to be ordered.
func partitionToolboxEndpointVars(state *State) {
	if len(state.MissingManualVars) == 0 || len(state.Toolboxes) == 0 {
		return
	}

	// keyToToolbox maps each declared toolbox's canonical endpoint key
	// to its ResourceRef. envkey.ToolboxMCPEndpoint is the single
	// source of truth for the key normalization (sanitize → upper →
	// "TOOLBOX_<X>_MCP_ENDPOINT") shared with the provisioner and the
	// local.toolboxes doctor check; computing the lookup here ensures
	// any future normalization change ripples consistently.
	keyToToolbox := make(map[string]ResourceRef, len(state.Toolboxes))
	for _, tb := range state.Toolboxes {
		keyToToolbox[envkey.ToolboxMCPEndpoint(tb.Name)] = tb
	}

	remaining := make([]string, 0, len(state.MissingManualVars))
	var matched []ResourceRef
	for _, name := range state.MissingManualVars {
		if tb, ok := keyToToolbox[name]; ok {
			matched = append(matched, tb)
			continue
		}
		remaining = append(remaining, name)
	}
	if len(matched) == 0 {
		return
	}

	state.MissingManualVars = remaining
	// Sort matched by (Name, ServiceName) for deterministic rendering;
	// state.Toolboxes is already sorted but `matched` was built by
	// MissingManualVars iteration order, which is sorted by var name
	// rather than toolbox name. Re-sort so callers see the same
	// ordering they'd see if they iterated state.Toolboxes directly.
	slices.SortFunc(matched, func(a, b ResourceRef) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ServiceName, b.ServiceName)
	})
	state.MissingToolboxEndpoints = matched
}

// populateOpenAPIPayload locates a sample invoke payload for the
// resolver. When a live probe is registered (via
// WithLiveOpenAPIProbe) the closure is consulted first and its
// non-empty body wins; otherwise — or on error / empty body — the
// on-disk cache produced by fetchOpenAPISpec is consulted. All
// failure modes (probe disabled, fetch error, cache miss, malformed
// spec, no extractable payload) leave state.HasOpenAPI false so the
// resolver can fall back to the protocol-generic literal.
//
// Live-fetch errors are silently absorbed: the doctor / `run` paths
// must not surface partial-network diagnostics here — the user's
// terminal is the wrong surface for them and a transient probe
// failure should never block the cached fallback.
func populateOpenAPIPayload(
	ctx context.Context,
	cfg *config,
	projectPath, envName string,
	state *State,
) {
	var specBytes []byte
	if cfg.openAPILiveFetch != nil {
		if b, err := cfg.openAPILiveFetch(ctx); err == nil && len(b) > 0 {
			specBytes = b
		}
	}
	if len(specBytes) == 0 {
		if cfg.openAPIAgent == "" || cfg.openAPISuffix == "" {
			return
		}
		configDir := filepath.Join(projectPath, ".azure", envName)
		b, err := ReadCachedOpenAPISpec(configDir, cfg.openAPIAgent, cfg.openAPISuffix)
		if err != nil || len(b) == 0 {
			return
		}
		specBytes = b
	}
	payload := ExtractInvokeExample(specBytes)
	if payload == "" {
		return
	}
	state.HasOpenAPI = true
	state.OpenAPIPayload = payload
}

// collectFoundry decodes each `host: microsoft.foundry` service entry once
// and populates state with:
//
//   - state.Services — one entry PER AGENT (the unit the resolvers show
//     and invoke), with the owning Foundry service name recorded in
//     ServiceName.
//   - state.ModelRefs / Toolboxes / Connections + Has* flags — the
//     aggregate Foundry resources declared across all Foundry services.
//   - state.HasProjectEndpoint — also set true when any Foundry service
//     declares an explicit `endpoint:` (reusing an existing project).
//
// The decode source is the service's AdditionalProperties (forwarded by
// core over gRPC); there is no on-disk re-parse of azure.yaml. Decode
// errors are surfaced into errs but never abort assembly — a partially
// populated config still yields useful guidance.
func collectFoundry(
	ctx context.Context,
	src Source,
	envName string,
	project *azdext.ProjectConfig,
	state *State,
	errs *[]error,
) {
	if project == nil || len(project.Services) == 0 {
		return
	}

	models := map[resourceKey]ResourceRef{}
	toolboxes := map[resourceKey]ResourceRef{}
	connections := map[resourceKey]ResourceRef{}

	var services []ServiceState
	for _, svc := range project.Services {
		if svc == nil || svc.Host != agentHost {
			continue
		}
		cfg, err := decodeFoundryService(svc.AdditionalProperties, project.Path)
		if err != nil {
			*errs = append(*errs, fmt.Errorf("decode service %q: %w", svc.Name, err))
		}
		if cfg.Endpoint != "" {
			state.HasProjectEndpoint = true
		}

		for _, agent := range cfg.Agents {
			if agent.Name == "" {
				continue
			}
			services = append(services, ServiceState{
				Name:         agent.Name,
				Kind:         agent.Kind,
				Host:         svc.Host,
				ServiceName:  svc.Name,
				Protocol:     preferredProtocol(agent.Protocols),
				RelativePath: agent.Project,
				IsDeployed:   isDeployed(ctx, src, envName, agent.Name, errs),
				Env:          agent.Env,
			})
		}

		for _, d := range cfg.Deployments {
			addResourceRef(models, svc.Name, d.Name, deploymentDetail(d))
		}
		for _, tb := range cfg.Toolboxes {
			addResourceRef(toolboxes, svc.Name, tb.Name, "")
		}
		for _, c := range cfg.Connections {
			addResourceRef(connections, svc.Name, c.Name, connectionDetail(c))
		}
	}

	slices.SortFunc(services, func(a, b ServiceState) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ServiceName, b.ServiceName)
	})
	state.Services = services

	state.ModelRefs = sortedResourceRefs(models)
	state.Toolboxes = sortedResourceRefs(toolboxes)
	state.Connections = sortedResourceRefs(connections)
	state.HasModels = len(state.ModelRefs) > 0
	state.HasToolboxes = len(state.Toolboxes) > 0
	state.HasConnections = len(state.Connections) > 0
}

// addResourceRef inserts a ResourceRef into m keyed on (service, name),
// skipping empty names and (service, name) duplicates so the same resource
// declared twice within one service collapses to one entry.
func addResourceRef(m map[resourceKey]ResourceRef, serviceName, name, detail string) {
	if name == "" {
		return
	}
	k := resourceKey{service: serviceName, name: name}
	if _, dup := m[k]; dup {
		return
	}
	m[k] = ResourceRef{Name: name, ServiceName: serviceName, Detail: detail}
}

// preferredProtocol picks the protocol the next-step hints use for an
// agent: ProtocolResponses wins over ProtocolInvocations (so the suggested
// payload works on the broadest set of agents). Empty when neither is
// declared.
func preferredProtocol(protocols []foundryProtocol) string {
	sawInvocations := false
	for _, p := range protocols {
		switch strings.TrimSpace(p.Protocol) {
		case ProtocolResponses:
			return ProtocolResponses
		case ProtocolInvocations:
			sawInvocations = true
		}
	}
	if sawInvocations {
		return ProtocolInvocations
	}
	return ""
}

// deploymentDetail renders the kind-specific ResourceRef.Detail for a model
// deployment: "<Format>/<Name>" when both are present, else whichever side
// is populated.
func deploymentDetail(d foundryDeployment) string {
	switch {
	case d.Model.Format != "" && d.Model.Name != "":
		return d.Model.Format + "/" + d.Model.Name
	case d.Model.Format != "":
		return d.Model.Format
	default:
		return d.Model.Name
	}
}

// connectionDetail renders the kind-specific identifier doctor remediation
// messages quote for a connection. Empty-category and empty-target entries
// fall back to whichever side is populated so we never emit a useless
// " | " separator with both halves blank.
func connectionDetail(c foundryConnection) string {
	switch {
	case c.Category != "" && c.Target != "":
		return c.Category + " | " + c.Target
	case c.Category != "":
		return c.Category
	default:
		return c.Target
	}
}

// resourceKey is the (service, name) dedup key for the per-kind resource
// maps populated by collectFoundry.
type resourceKey struct {
	service string
	name    string
}

// sortedResourceRefs flattens the dedup map into a slice sorted by Name
// (ties broken by ServiceName). The determinism is load-bearing for doctor
// output snapshots and downstream display.
func sortedResourceRefs(m map[resourceKey]ResourceRef) []ResourceRef {
	if len(m) == 0 {
		return nil
	}
	out := make([]ResourceRef, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b ResourceRef) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ServiceName, b.ServiceName)
	})
	return out
}

// detectMissingVars scans each agent's `env` map (from azure.yaml) and
// partitions the unset ${VAR} references into two lists:
//
//  1. infra:  unset ${VAR} refs that name a top-level output of
//     <projectPath>/infra/main.bicep (provision outputs)
//  2. manual: unset ${VAR} refs that do NOT name a Bicep output (user
//     inputs the user must `azd env set`)
//
// Only bare-form ${VAR} refs participate: when the author supplies an
// explicit fallback via `${VAR:-default}`, the deploy-time resolver
// substitutes the fallback and the variable is not required.
// `extractEnvRefs` filters defaulted refs out. ${{...}} Foundry
// server-side references are not matched and never surface here.
//
// Classification rule for ${VAR}: a variable is an infra var iff its name
// is declared as a top-level `output` in `<projectPath>/infra/main.bicep`.
// azd's Bicep provider writes those output names verbatim into
// `.azure/<env>/.env` after `azd provision` succeeds, so set membership is
// a precise signal of "this variable is provided by `azd provision`."
// Everything else is treated as a user-supplied manual variable that the
// user must set via `azd env set`.
//
// When `infra/main.bicep` is missing or declares no outputs, the
// Bicep-output set is empty and every unresolved bare ref lands in the
// manual bucket. This is the conservative answer: the resolver emits
// `azd env set <NAME> <value>` hints, which a user can always follow.
//
// Both result lists are deduplicated and sorted ascending. Transport
// errors from src.EnvValue are appended to errs so AssembleState's caller
// can surface them in --debug logs without aborting the snapshot.
func detectMissingVars(
	ctx context.Context,
	src Source,
	envName, projectPath string,
	services []ServiceState,
	errs *[]error,
) (infra, manual []string) {
	if envName == "" || projectPath == "" || len(services) == 0 {
		return nil, nil
	}

	bicepOutputs := bicepOutputSet(projectPath)
	seenInfra := make(map[string]struct{})
	seenManual := make(map[string]struct{})

	for _, svc := range services {
		for _, name := range extractEnvRefs(svc.Env) {
			if _, ok := seenInfra[name]; ok {
				continue
			}
			if _, ok := seenManual[name]; ok {
				continue
			}
			value, err := src.EnvValue(ctx, envName, name)
			if err != nil {
				*errs = append(*errs, fmt.Errorf("read %s: %w", name, err))
				continue
			}
			if value != "" {
				continue
			}
			if _, isBicepOutput := bicepOutputs[name]; isBicepOutput {
				seenInfra[name] = struct{}{}
			} else {
				seenManual[name] = struct{}{}
			}
		}
	}

	infra = slices.Sorted(maps.Keys(seenInfra))
	manual = slices.Sorted(maps.Keys(seenManual))
	return infra, manual
}

// bicepOutputSet returns the Bicep-output names declared by
// <projectPath>/infra/main.bicep as a lookup set. Best-effort: a
// missing file, malformed content, or zero outputs return an empty
// (but non-nil) map so callers can use the idiomatic `_, ok := set[k]`
// form without nil-guarding.
func bicepOutputSet(projectPath string) map[string]struct{} {
	names := discoverBicepOutputs(projectPath)
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return set
}

// extractEnvRefs returns the unique bare-form ${VAR} names referenced in an
// agent's env map. Refs that carry a `:-default` fallback are skipped (the
// deploy-time expander honors the default, so the variable is not
// required). ${{...}} Foundry server-side references are not matched by the
// pattern and are therefore ignored.
func extractEnvRefs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var refs []string
	for _, value := range env {
		for _, m := range envVarRefPattern.FindAllStringSubmatch(value, -1) {
			if m[2] != "" {
				// Variable carries an explicit `:-fallback` default; the
				// deploy-time resolver honors it, so the user does not need
				// to set the var. Skipping here keeps the next-step hint
				// honest: only bare-form refs become missing-var prompts.
				continue
			}
			name := m[1]
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			refs = append(refs, name)
		}
	}
	return refs
}

// isDeployed reports whether the named agent has a recorded deploy version
// in the active environment (AGENT_<KEY>_VERSION non-empty).
//
// TODO(unify true-up): under the unified design the deploy marker is
// per-agent. The exact env-var convention the new microsoft.foundry service
// target writes is owned by that rework (PR #8590 §2.6/§2.8) and is not yet
// pinned. We assume AGENT_<agentKey>_VERSION here, mirroring the legacy
// per-service writer; verify and adjust against the service target once it
// lands.
func isDeployed(
	ctx context.Context,
	src Source,
	envName, agentName string,
	errs *[]error,
) bool {
	if envName == "" || agentName == "" {
		return false
	}
	key := fmt.Sprintf(agentVersionVarFormat, serviceKey(agentName))
	value, err := src.EnvValue(ctx, envName, key)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("read %s: %w", key, err))
		return false
	}
	return value != ""
}

// serviceKey converts an agent name into the env-var key fragment used by
// the deploy-time env-var writer in service_target_agent.go. It mirrors
// AgentServiceTargetProvider.getServiceKey verbatim.
func serviceKey(name string) string {
	k := strings.ReplaceAll(name, " ", "_")
	k = strings.ReplaceAll(k, "-", "_")
	return strings.ToUpper(k)
}

// parsePendingProvisionReasons splits the AI_AGENT_PENDING_PROVISION
// env-var value into a sorted, deduplicated, whitespace-trimmed list of
// reason tags. Empty input or input containing only separators returns
// nil. Malformed input is best-effort normalized — the env var is a
// hint signal and parse trouble should not abort state assembly. This
// helper mirrors cmd.parsePendingProvisionReasons; the duplication is
// intentional to keep nextstep a leaf package with no dependency on cmd.
func parsePendingProvisionReasons(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	seen := make(map[string]struct{})
	for raw := range strings.SplitSeq(value, ",") {
		tag := strings.TrimSpace(raw)
		if tag == "" {
			continue
		}
		seen[tag] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for tag := range seen {
		out = append(out, tag)
	}
	slices.Sort(out)
	return out
}
