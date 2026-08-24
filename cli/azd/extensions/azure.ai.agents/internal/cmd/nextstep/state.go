// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/agentkind"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	// agentHost matches the value set in azure.yaml for an azure.ai.agent
	// service. Duplicated here (rather than imported from the parent cmd
	// package) so nextstep stays free of upward dependencies; Phase 2 will
	// wire cmd → nextstep, so the reverse import would close a cycle.
	agentHost = "azure.ai.agent"

	// agentVersionVarFormat is the env-var name that signals a deployed
	// agent service. Filled with the upper-cased service key.
	agentVersionVarFormat = "AGENT_%s_VERSION"

	// agentEndpointVarFormat is the base endpoint env-var written for every
	// deployed agent. Voice agents (kind: prompt-voice) use it as the deploy
	// completion marker. Unified voice deploys also set VERSION, while legacy
	// voice environments may have only this endpoint marker.
	agentEndpointVarFormat = "AGENT_%s_ENDPOINT"

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

// placeholderPattern aliases agent_yaml.PlaceholderPattern. nextstep
// surfaces the same placeholders that agent_yaml's
// injectParameterValues warns about, so the two MUST stay in lockstep.
// Keeping a single shared regex (defined in agent_yaml, where the
// substitution logic lives) makes that constraint explicit and avoids
// drift if the placeholder syntax is ever broadened again. See
// agent_yaml/placeholders.go for the full rationale on the regex
// shape (hyphens, dots, whitespace in capture group).
var placeholderPattern = agent_yaml.PlaceholderPattern

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
	// ServiceConfigValue returns a raw service configuration value.
	ServiceConfigValue(
		ctx context.Context,
		serviceName string,
		path string,
	) (*structpb.Value, bool, error)
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

func (s *clientSource) ServiceConfigValue(
	ctx context.Context,
	serviceName string,
	path string,
) (*structpb.Value, bool, error) {
	resp, err := s.client.Project().GetServiceConfigValue(
		ctx,
		&azdext.GetServiceConfigValueRequest{
			ServiceName: serviceName,
			Path:        path,
		},
	)
	if err != nil {
		return nil, false, err
	}
	if resp == nil {
		return nil, false, nil
	}
	return resp.Value, resp.Found, nil
}

// Option configures AssembleState.
type Option func(*config)

type config struct {
	// environmentName, when non-empty, selects the environment to inspect
	// without changing azd's active environment.
	environmentName string

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

// WithEnvironment selects the azd environment used to assemble next-step
// state. It does not change the project's active environment.
func WithEnvironment(name string) Option {
	return func(c *config) { c.environmentName = name }
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
	state.EnvironmentName = cfg.environmentName
	var errs []error

	envName := cfg.environmentName
	if envName == "" {
		var err error
		envName, err = src.CurrentEnvName(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("read current environment: %w", err))
		}
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

	state.Services = collectServices(
		ctx,
		src,
		envName,
		project,
		&errs,
		&state.EnvironmentLoadErrors,
	)

	var splitToolboxState splitToolboxResult
	if project != nil {
		if len(state.Services) > 0 {
			populateManifestResources(project.Path, state)
		}
		splitToolboxState = populateSplitToolboxes(
			ctx,
			src,
			envName,
			project,
			state,
			&errs,
		)
	}

	if project != nil && envName != "" {
		state.MissingInfraVars, state.MissingManualVars, state.UnresolvedPlaceholders =
			detectMissingVars(
				ctx,
				src,
				envName,
				project.Path,
				state.Services,
				state.Toolboxes,
				splitToolboxState.endpointKeys,
				splitToolboxState.excludedAgents,
				&errs,
			)
		populateOpenAPIPayload(ctx, cfg, project.Path, envName, state)
	}

	if envName != "" && len(state.Toolboxes) > 0 {
		state.MissingToolboxEndpoints = probeToolboxEndpoints(
			ctx, src, envName, state.Toolboxes, &state.ToolboxEndpointErrors, &errs)
		state.ToolboxEndpointsChecked = true
	}

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

// probeToolboxEndpoints reads each canonical toolbox endpoint once.
// azd produces these values, so the probe does not depend on agent
// environment references.
func probeToolboxEndpoints(
	ctx context.Context,
	src Source,
	envName string,
	toolboxes []ResourceRef,
	endpointErrors *[]string,
	errs *[]error,
) []ResourceRef {
	seen := make(map[string]struct{}, len(toolboxes))
	var missing []ResourceRef
	for _, toolbox := range toolboxes {
		key := envkey.ToolboxMCPEndpoint(toolbox.Name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		value, err := src.EnvValue(ctx, envName, key)
		if err != nil {
			probeErr := fmt.Errorf("read toolbox endpoint %s: %w", key, err)
			*endpointErrors = append(*endpointErrors, probeErr.Error())
			*errs = append(*errs, probeErr)
			continue
		}
		if strings.TrimSpace(value) == "" {
			missing = append(missing, toolbox)
		}
	}
	slices.SortFunc(missing, func(a, b ResourceRef) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.ServiceName, b.ServiceName)
	})
	slices.Sort(*endpointErrors)
	return missing
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

func collectServices(
	ctx context.Context,
	src Source,
	envName string,
	project *azdext.ProjectConfig,
	errs *[]error,
	environmentLoadErrors *[]string,
) []ServiceState {
	if project == nil || len(project.Services) == 0 {
		return nil
	}

	services := make([]ServiceState, 0, len(project.Services))
	for _, svc := range project.Services {
		if svc == nil || svc.Host != agentHost {
			continue
		}
		protocol, multiProtocol := loadServiceProtocolInfo(project.Path, svc)
		environment, err := loadEffectiveAgentEnvironment(svc, project.Path)
		if err != nil {
			loadErr := fmt.Errorf(
				"service %q agent environment: %w",
				svc.Name,
				err,
			)
			*errs = append(*errs, loadErr)
			*environmentLoadErrors = append(
				*environmentLoadErrors,
				loadErr.Error(),
			)
		}
		services = append(services, ServiceState{
			Name:              svc.Name,
			Host:              svc.Host,
			RelativePath:      svc.RelativePath,
			Protocol:          protocol,
			MultiProtocol:     multiProtocol,
			IsDeployed:        isDeployed(ctx, src, envName, svc.Name, isVoiceService(project.Path, svc), errs),
			EnvironmentValues: sortedEnvironmentValues(environment),
		})
	}

	slices.SortFunc(services, func(a, b ServiceState) int {
		return strings.Compare(a.Name, b.Name)
	})
	slices.Sort(*environmentLoadErrors)
	return services
}

// loadServiceProtocol returns the protocol the service's agent.yaml declares
// for next-step hint purposes. The lookup is best-effort: missing or
// malformed manifests, empty protocols sections, or any I/O error all return
// an empty string, and the resolver falls back to ProtocolResponses. When the
// manifest declares multiple protocols, ProtocolResponses wins over
// ProtocolInvocations so the suggested payload works on the broadest set of
// agents.
func loadServiceProtocol(projectPath string, svc *azdext.ServiceConfig) string {
	protocol, _ := loadServiceProtocolInfo(projectPath, svc)
	return protocol
}

func loadServiceProtocolInfo(projectPath string, svc *azdext.ServiceConfig) (string, bool) {
	if protocol, multiProtocol := loadServiceProtocolFromConfig(svc); protocol != "" {
		return protocol, multiProtocol
	}
	if svc == nil {
		return "", false
	}
	return loadServiceProtocolFromFile(projectPath, svc.RelativePath)
}

func loadServiceProtocolFromConfig(svc *azdext.ServiceConfig) (string, bool) {
	props := nextStepServiceConfigProps(svc)
	if len(props) == 0 {
		return "", false
	}
	data, err := yaml.Marshal(props)
	if err != nil {
		return "", false
	}
	return loadServiceProtocolFromBytes(data)
}

func nextStepServiceConfigProps(svc *azdext.ServiceConfig) map[string]any {
	if svc == nil {
		return nil
	}
	inline := svc.GetAdditionalProperties()
	if structHasKind(inline) {
		return inline.AsMap()
	}
	cfg := svc.GetConfig()
	if structHasKind(cfg) {
		return cfg.AsMap()
	}
	return nil
}

func structHasKind(s *structpb.Struct) bool {
	if s == nil {
		return false
	}
	v, ok := s.GetFields()["kind"]
	return ok && strings.TrimSpace(v.GetStringValue()) != ""
}

// isVoiceService reports whether the service declares kind: prompt-voice. It
// delegates to the shared agentkind lookup so the next-step reader classifies a
// service identically to the deploy path and Endpoints, including honoring an
// explicit AGENT_DEFINITION_PATH override (which deploy follows). Kind
// resolution is best-effort for next-step hints, so any error is treated as
// not-voice.
func isVoiceService(projectPath string, svc *azdext.ServiceConfig) bool {
	isVoice, err := agentkind.IsPromptVoice(svc, projectPath, os.Getenv("AGENT_DEFINITION_PATH"))
	return err == nil && isVoice
}

func loadServiceProtocolFromFile(projectPath, relativePath string) (string, bool) {
	if projectPath == "" {
		return "", false
	}
	manifestPath, err := paths.JoinAllowRoot(projectPath, relativePath, "agent.yaml")
	if err != nil {
		return "", false
	}
	data, err := os.ReadFile(manifestPath) //nolint:gosec // path is validated under the project root
	if err != nil {
		return "", false
	}
	return loadServiceProtocolFromBytes(data)
}

func loadServiceProtocolFromBytes(data []byte) (string, bool) {
	var hosted agent_yaml.ContainerAgent
	if err := yaml.Unmarshal(data, &hosted); err != nil {
		return "", false
	}
	multiProtocol := len(hosted.Protocols) > 1

	sawInvocations := false
	sawActivity := false
	sawInvocationsWS := false
	for _, p := range hosted.Protocols {
		switch strings.TrimSpace(p.Protocol) {
		case ProtocolResponses:
			return ProtocolResponses, multiProtocol
		case ProtocolInvocationsWS:
			sawInvocationsWS = true
		case ProtocolInvocations:
			sawInvocations = true
		case ProtocolActivity, ProtocolActivityLegacy:
			sawActivity = true
		}
	}
	if sawInvocations {
		return ProtocolInvocations, multiProtocol
	}
	if sawActivity {
		return ProtocolActivity, multiProtocol
	}
	if sawInvocationsWS {
		return ProtocolInvocationsWS, multiProtocol
	}
	return "", multiProtocol
}

// detectMissingVars walks each service's effective environment values
// section and partitions the trouble-spots into three lists:
//
//  1. infra:        unset ${VAR} refs that name a top-level output of
//     <projectPath>/infra/main.bicep (provision outputs)
//  2. manual:       unset ${VAR} refs that do NOT name a Bicep output
//     (user inputs the user must `azd env set`)
//  3. placeholders: surviving {{NAME}} Mustache placeholders (init failed
//     to substitute these from agent.manifest.yaml's parameters block)
//
// Only bare-form ${VAR} refs participate in (1) and (2): when the
// configuration author supplies an explicit fallback via
// `${VAR:-default}`,
// the deploy-time resolver substitutes the fallback, so the variable
// is not required. `extractEnvironmentRefs` filters defaulted refs
// out.
//
// Classification rule for ${VAR}: a variable is an infra var iff its
// name is declared as a top-level `output` in `<projectPath>/infra/
// main.bicep`. azd's Bicep provider writes those output names verbatim
// into `.azure/<env>/.env` after `azd provision` succeeds (see
// cli/azd/pkg/infra/provisioning/bicep/bicep_provider.go around the
// `outputs[key] = ...` write and pkg/infra/provisioning/manager.go's
// `UpdateEnvironment` → `env.DotenvSet(key, ...)`), so set membership
// is a precise signal of "this variable is provided by `azd provision`."
// Everything else is treated as a user-supplied manual variable that
// the user must set via `azd env set`. This mirrors the spec wording in
// issue #7975 ("Walk azure.yaml service configs; collect ${...}
// references that map to known Bicep outputs").
//
// When `infra/main.bicep` is missing or declares no outputs, the
// Bicep-output set is empty and every unresolved bare ref lands in the
// manual bucket. This is the conservative answer: the resolver will
// emit `azd env set <NAME> <value>` hints, which a user can always
// follow. If the project is actually backed by a Bicep template whose
// outputs are not yet declared, the fix is to declare the missing
// output — not to guess based on the variable name.
//
// {{NAME}} placeholders are reported separately because the user cannot
// fix them with `azd env set` — the placeholder would land in the
// container literally at deploy time. The resolver surfaces an
// "edit agent configuration" suggestion for each.
//
// All three result lists are deduplicated and sorted ascending.
// Transport errors from src.EnvValue are appended to errs so
// AssembleState's caller can surface them in --debug logs without
// aborting the snapshot.
func detectMissingVars(
	ctx context.Context,
	src Source,
	envName, projectPath string,
	services []ServiceState,
	toolboxes []ResourceRef,
	splitToolboxEndpointKeys map[string]struct{},
	excludedAgents map[string]struct{},
	errs *[]error,
) (infra, manual, placeholders []string) {
	if envName == "" || projectPath == "" || len(services) == 0 {
		return nil, nil, nil
	}

	bicepOutputs := bicepOutputSet(projectPath)
	seenInfra := make(map[string]struct{})
	seenManual := make(map[string]struct{})
	seenPlaceholder := make(map[string]struct{})
	toolboxKeys := make(map[string]struct{}, len(toolboxes))
	for _, toolbox := range toolboxes {
		toolboxKeys[envkey.ToolboxMCPEndpoint(toolbox.Name)] = struct{}{}
	}
	for key := range splitToolboxEndpointKeys {
		toolboxKeys[key] = struct{}{}
	}

	for _, svc := range services {
		if _, excluded := excludedAgents[svc.Name]; excluded {
			continue
		}
		refs, phs := extractEnvironmentRefs(svc.EnvironmentValues)
		for _, name := range refs {
			if _, isToolbox := toolboxKeys[name]; isToolbox {
				continue
			}
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
		for _, name := range phs {
			seenPlaceholder[name] = struct{}{}
		}
	}

	infra = slices.Sorted(maps.Keys(seenInfra))
	manual = slices.Sorted(maps.Keys(seenManual))
	placeholders = slices.Sorted(maps.Keys(seenPlaceholder))
	return infra, manual, placeholders
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

// extractEnvironmentRefs returns two lists from effective values:
//
//  1. refs: unique bare-form ${VAR} names. Refs that supply a fallback
//     via `${VAR:-default}` are skipped — the deploy-time expander
//     honors the default, so the variable is not required and never
//     warrants a missing-var hint.
//  2. placeholders: unique {{NAME}} Mustache-style placeholders that
//     init's manifest processing failed to substitute. These would land
//     in the container literally as `{{NAME}}` at deploy time.
//
// Order matches the sorted environment-variable names. Foundry
// expressions are excluded from placeholder detection.
func extractEnvironmentRefs(values []string) (refs, placeholders []string) {
	seenRef := make(map[string]struct{})
	seenPh := make(map[string]struct{})
	for _, value := range values {
		for _, ref := range synthesis.FindEnvReferences(value) {
			if ref.HasDefault {
				// The deploy-time expander supplies a fallback, so the
				// environment variable is not required and must not become a
				// missing-var hint. This matches the azd resolver semantics
				// shared by the extension's other env-ref consumers.
				continue
			}
			name := ref.Name
			if _, ok := seenRef[name]; ok {
				continue
			}
			seenRef[name] = struct{}{}
			refs = append(refs, name)
		}
		for _, m := range placeholderPattern.FindAllStringSubmatchIndex(value, -1) {
			if len(m) < 4 || (m[0] > 0 && value[m[0]-1] == '$') {
				continue
			}
			name := value[m[2]:m[3]]
			if _, ok := seenPh[name]; ok {
				continue
			}
			seenPh[name] = struct{}{}
			placeholders = append(placeholders, name)
		}
	}
	return refs, placeholders
}

func isDeployed(
	ctx context.Context,
	src Source,
	envName, serviceName string,
	isVoice bool,
	errs *[]error,
) bool {
	if envName == "" || serviceName == "" {
		return false
	}
	key := fmt.Sprintf(agentVersionVarFormat, serviceKey(serviceName))
	value, err := src.EnvValue(ctx, envName, key)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("read %s: %w", key, err))
		return false
	}
	if !isVoice {
		return value != ""
	}

	// Voice deploys use the base ENDPOINT env var as the completion marker.
	// Unified voice deploys write VERSION before ENDPOINT to keep ENDPOINT as the
	// final marker.
	// Require ENDPOINT for voice even when VERSION is present, otherwise a partial
	// env write could be reported as deployed before the callable endpoint was
	// persisted. Gate this on the service's actual declared kind: a hosted agent
	// whose deploy partially failed can also present an empty VERSION with a
	// lingering ENDPOINT, and must stay reported as not-deployed. This mirrors the
	// kind gate in
	// AgentServiceTargetProvider.Endpoints (project package); the two live in
	// separate packages because project imports nextstep, so a literally shared
	// helper would create an import cycle.
	endpointKey := fmt.Sprintf(agentEndpointVarFormat, serviceKey(serviceName))
	endpointValue, err := src.EnvValue(ctx, envName, endpointKey)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("read %s: %w", endpointKey, err))
		return false
	}
	return endpointValue != ""
}

// serviceKey converts a service name into the env-var key fragment used by
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
