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
	"regexp"
	"slices"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
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

	// projectEndpointVar is the env-var that carries the Foundry project
	// endpoint URL produced by `azd ai agent init`.
	projectEndpointVar = "AZURE_AI_PROJECT_ENDPOINT"

	// useExistingAIProjectVar records the user's choice in the
	// `azd ai agent init` model-configuration step. "true" means the
	// user selected an existing Foundry project (init populated
	// AZURE_AI_PROJECT_ENDPOINT and related vars immediately from that
	// project); "false" means the user opted to create a new Foundry
	// project, which requires `azd provision` to run before any
	// AZURE_AI_PROJECT_ENDPOINT value reflects reality. The variable
	// also drives Bicep's "skip project creation" branch — see
	// USE_EXISTING_AI_PROJECT in CHANGELOG.md entry for PR #7843.
	useExistingAIProjectVar = "USE_EXISTING_AI_PROJECT"

	// pendingProvisionVar names the extension-owned env var that
	// lists resource-class tags init configured but provision has
	// not yet materialized. See State.PendingProvisionReasons for
	// the full semantics and pending_provision.go in the cmd package
	// for the read/write helpers and the reason-tag taxonomy. The
	// constant is duplicated here (rather than imported from cmd)
	// because nextstep is a leaf package with no dependency on cmd
	// — both packages share the same string literal contract.
	pendingProvisionVar = "AI_AGENT_PENDING_PROVISION"

	// azureInfraPrefix tags an env-var name as an azd-infra output rather
	// than a user-supplied manual variable. Outputs of `azd provision`
	// in the AI Foundry templates uniformly start with this prefix
	// (AZURE_AI_PROJECT_*, AZURE_OPENAI_*, AZURE_SUBSCRIPTION_*, etc.),
	// so the prefix doubles as the classification heuristic.
	azureInfraPrefix = "AZURE_"
)

// envVarRefPattern captures ${VAR} references inside YAML string values.
// Group 1 is the variable name. Group 2 captures the optional default
// tail `:-fallback`; when group 2 is non-empty the agent.yaml author
// explicitly opted into a fallback and the variable is therefore not
// required at deploy time (the runtime expander `drone/envsubst` honors
// `:-` semantics). `extractAgentYamlEnvRefs` skips refs with a non-empty
// group 2 so they never surface in the missing-vars hints; the variable
// is reported as missing only when authored as the bare `${VAR}` form.
// Variable names follow the standard shell convention: leading letter or
// underscore, then alphanumeric or underscore.
var envVarRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-[^}]*)?\}`)

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
	authProbe bool

	// openAPIAgent and openAPISuffix together enable a cache-only OpenAPI
	// payload lookup. The zero value (empty strings) disables the probe.
	openAPIAgent  string
	openAPISuffix string
}

// WithAuthProbe enables a token-introspection step that populates
// State.IsAuthenticated. Default false. Only the full-sweep doctor path
// should enable this; every other resolver receives AuthUnknown and
// suppresses login-prompt advice in success paths.
func WithAuthProbe(enabled bool) Option {
	return func(c *config) { c.authProbe = enabled }
}

// WithOpenAPIProbe enables a cache-only OpenAPI lookup that populates
// State.OpenAPIPayload with a sample invoke payload extracted from the most
// recent on-disk cache for (agentName, suffix). suffix is "local" or
// "remote", matching fetchOpenAPISpec's filename convention.
//
// When agentName or suffix is empty the probe is disabled (the zero value).
// The probe is strictly cache-only: it never contacts the network. The
// cache is produced by `azd ai agent invoke` (and future `run` callers)
// when they fetch the agent's OpenAPI spec. On cache miss, malformed
// spec, or any read error the probe leaves State.HasOpenAPI false and
// the resolver falls back to the protocol-generic <payload> literal.
func WithOpenAPIProbe(agentName, suffix string) Option {
	return func(c *config) {
		c.openAPIAgent = agentName
		c.openAPISuffix = suffix
	}
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

		// USE_EXISTING_AI_PROJECT is the explicit signal `azd ai agent
		// init` writes to record the user's deploy-vs-existing choice.
		// When the user just selected "Deploy new model(s)" (value
		// "false"), the Foundry project does not exist yet — any
		// AZURE_AI_PROJECT_ENDPOINT value carried over from a prior
		// init run or a sibling environment is stale and must not let
		// the post-init resolver mistake the state for "ready to run
		// or deploy". The flag is only set for the literal string
		// "false"; an unset variable (no init yet) or "true" both
		// leave the flag false so existing resolver heuristics drive
		// the decision.
		useExisting, err := src.EnvValue(ctx, envName, useExistingAIProjectVar)
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", useExistingAIProjectVar, err))
		}
		state.NeedsAIProjectProvision = useExisting == "false"

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
	}

	project, err := src.Project(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("load project: %w", err))
	}

	state.Services = collectServices(ctx, src, envName, project, &errs)

	if project != nil && envName != "" {
		state.MissingInfraVars, state.MissingManualVars, state.UnresolvedPlaceholders = detectMissingVars(
			ctx, src, envName, project.Path, state.Services, &errs,
		)
		populateOpenAPIPayload(cfg, project.Path, envName, state)
	}

	// authProbe lands in a later commit; the flag is already plumbed so
	// call sites and tests can be written against the final API.
	_ = cfg.authProbe

	return state, errs
}

// populateOpenAPIPayload reads the on-disk OpenAPI cache produced by
// fetchOpenAPISpec and extracts a sample invoke payload. All failure
// modes (probe disabled, cache miss, malformed spec, no extractable
// payload) leave state.HasOpenAPI false so the resolver can fall back
// to the protocol-generic literal.
func populateOpenAPIPayload(cfg *config, projectPath, envName string, state *State) {
	if cfg.openAPIAgent == "" || cfg.openAPISuffix == "" {
		return
	}
	configDir := filepath.Join(projectPath, ".azure", envName)
	specBytes, err := ReadCachedOpenAPISpec(configDir, cfg.openAPIAgent, cfg.openAPISuffix)
	if err != nil || len(specBytes) == 0 {
		return
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
) []ServiceState {
	if project == nil || len(project.Services) == 0 {
		return nil
	}

	services := make([]ServiceState, 0, len(project.Services))
	for _, svc := range project.Services {
		if svc == nil || svc.Host != agentHost {
			continue
		}
		services = append(services, ServiceState{
			Name:         svc.Name,
			Host:         svc.Host,
			RelativePath: svc.RelativePath,
			Protocol:     loadServiceProtocol(project.Path, svc.RelativePath),
			IsDeployed:   isDeployed(ctx, src, envName, svc.Name, errs),
		})
	}

	slices.SortFunc(services, func(a, b ServiceState) int {
		return strings.Compare(a.Name, b.Name)
	})
	return services
}

// loadServiceProtocol returns the protocol the service's agent.yaml declares
// for next-step hint purposes. The lookup is best-effort: missing or
// malformed manifests, empty protocols sections, or any I/O error all return
// an empty string, and the resolver falls back to ProtocolResponses. When the
// manifest declares multiple protocols, ProtocolResponses wins over
// ProtocolInvocations so the suggested payload works on the broadest set of
// agents.
func loadServiceProtocol(projectPath, relativePath string) string {
	if projectPath == "" || relativePath == "" {
		return ""
	}
	manifestPath := filepath.Join(projectPath, relativePath, "agent.yaml")
	//nolint:gosec // G304: path constructed from azd project root, not user input.
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return ""
	}
	var hosted agent_yaml.ContainerAgent
	if err := yaml.Unmarshal(data, &hosted); err != nil {
		return ""
	}

	sawInvocations := false
	for _, p := range hosted.Protocols {
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

// detectMissingVars walks each service's agent.yaml environment_variables
// section and partitions the trouble-spots into three lists:
//
//  1. infra:        unset ${VAR} refs starting with AZURE_ (provision outputs)
//  2. manual:       unset ${VAR} refs not starting with AZURE_ (user inputs)
//  3. placeholders: surviving {{NAME}} Mustache placeholders (init failed
//     to substitute these from agent.manifest.yaml's parameters block)
//
// Only bare-form ${VAR} refs participate in (1) and (2): when the
// agent.yaml author supplies an explicit fallback via `${VAR:-default}`,
// the deploy-time resolver substitutes the fallback and the variable is
// not required. `extractAgentYamlEnvRefs` filters defaulted refs out.
//
// Classification heuristic for ${VAR}: variable names starting with
// "AZURE_" are treated as `azd provision` outputs (the AI Foundry
// templates produce names like AZURE_AI_PROJECT_ENDPOINT,
// AZURE_OPENAI_ENDPOINT, etc.); everything else is treated as a
// user-supplied manual variable. The heuristic is deliberately coarse —
// over-classifying a manual variable as infra at worst points the user
// at `azd provision` instead of `azd env set`, and the inverse
// misclassification still yields a usable hint.
//
// {{NAME}} placeholders are reported separately because the user cannot
// fix them with `azd env set` — the placeholder is literally inside
// agent.yaml and would land in the container as `{{NAME}}` at deploy
// time. The resolver surfaces an "edit agent.yaml" suggestion for each.
//
// All three result lists are deduplicated and sorted ascending. Read
// errors on individual agent.yaml files are silent: the resolver should
// fall back to the default branch rather than emit guidance that
// mentions variables we cannot prove are needed. Transport errors from
// src.EnvValue are appended to errs so AssembleState's caller can
// surface them in --debug logs without aborting the snapshot.
func detectMissingVars(
	ctx context.Context,
	src Source,
	envName, projectPath string,
	services []ServiceState,
	errs *[]error,
) (infra, manual, placeholders []string) {
	if envName == "" || projectPath == "" || len(services) == 0 {
		return nil, nil, nil
	}

	seenInfra := make(map[string]struct{})
	seenManual := make(map[string]struct{})
	seenPlaceholder := make(map[string]struct{})

	for _, svc := range services {
		refs, phs := extractAgentYamlEnvRefs(projectPath, svc.RelativePath)
		for _, name := range refs {
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
			if strings.HasPrefix(name, azureInfraPrefix) {
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

// extractAgentYamlEnvRefs returns two lists from the service's
// agent.yaml environment_variables block:
//
//  1. refs: unique bare-form ${VAR} names. Refs that supply a fallback
//     via `${VAR:-default}` are skipped — the deploy-time expander
//     honors the default, so the variable is not required and never
//     warrants a missing-var hint.
//  2. placeholders: unique {{NAME}} Mustache-style placeholders that
//     init's manifest processing failed to substitute. These would land
//     in the container literally as `{{NAME}}` at deploy time.
//
// Order matches first appearance in the file. Missing or malformed
// manifests return nil for both — consistent with loadServiceProtocol's
// best-effort contract.
func extractAgentYamlEnvRefs(projectPath, relativePath string) (refs, placeholders []string) {
	if projectPath == "" || relativePath == "" {
		return nil, nil
	}
	manifestPath := filepath.Join(projectPath, relativePath, "agent.yaml")
	//nolint:gosec // G304: path constructed from azd project root, not user input.
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, nil
	}
	var hosted agent_yaml.ContainerAgent
	if err := yaml.Unmarshal(data, &hosted); err != nil {
		return nil, nil
	}
	if hosted.EnvironmentVariables == nil {
		return nil, nil
	}

	seenRef := make(map[string]struct{})
	seenPh := make(map[string]struct{})
	for _, ev := range *hosted.EnvironmentVariables {
		for _, m := range envVarRefPattern.FindAllStringSubmatch(ev.Value, -1) {
			if m[2] != "" {
				// Variable carries an explicit `:-fallback` default; the
				// deploy-time resolver honors it, so the user does not need
				// to set the var. Skipping here keeps the next-step hint
				// honest: only bare-form refs become missing-var prompts.
				continue
			}
			name := m[1]
			if _, ok := seenRef[name]; ok {
				continue
			}
			seenRef[name] = struct{}{}
			refs = append(refs, name)
		}
		for _, m := range placeholderPattern.FindAllStringSubmatch(ev.Value, -1) {
			name := m[1]
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
	return value != ""
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
	for _, raw := range strings.Split(value, ",") {
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
