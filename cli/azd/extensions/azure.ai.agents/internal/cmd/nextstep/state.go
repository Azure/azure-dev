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
// Production callers use AssembleState; tests use this to inject a fake
// Source and exercise the resolver wiring without spinning up a real
// azd gRPC client.
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
	}

	project, err := src.Project(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("load project: %w", err))
	}

	state.Services = collectServices(ctx, src, envName, project, &errs)

	if project != nil && envName != "" {
		state.MissingInfraVars, state.MissingManualVars = detectMissingVars(
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
// section, extracts ${VAR} references, and partitions the unset names
// into infra-output and manual-input lists.
//
// Only bare-form refs (`${VAR}`) participate: when the agent.yaml author
// supplies an explicit fallback via `${VAR:-default}`, the deploy-time
// resolver substitutes the fallback and the variable is not required.
// `extractAgentYamlEnvRefs` filters defaulted refs out before they reach
// the classification step.
//
// Classification heuristic: variable names starting with "AZURE_" are
// treated as `azd provision` outputs (the AI Foundry templates produce
// names like AZURE_AI_PROJECT_ENDPOINT, AZURE_OPENAI_ENDPOINT, etc.);
// everything else is treated as a user-supplied manual variable. The
// heuristic is deliberately coarse — over-classifying a manual variable
// as infra at worst points the user at `azd provision` instead of
// `azd env set`, and the inverse misclassification still yields a
// usable hint.
//
// Both result lists are deduplicated and sorted ascending. Read errors
// on individual agent.yaml files are silent: the resolver should fall
// back to the default branch rather than emit guidance that mentions
// variables we cannot prove are needed. Transport errors from
// src.EnvValue are appended to errs so AssembleState's caller can
// surface them in --debug logs without aborting the snapshot.
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

	seenInfra := make(map[string]struct{})
	seenManual := make(map[string]struct{})

	for _, svc := range services {
		refs := extractAgentYamlEnvRefs(projectPath, svc.RelativePath)
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
	}

	infra = slices.Sorted(maps.Keys(seenInfra))
	manual = slices.Sorted(maps.Keys(seenManual))
	return infra, manual
}

// extractAgentYamlEnvRefs returns the unique bare-form ${VAR} names
// referenced in the service's agent.yaml environment_variables block.
// Refs that supply a fallback via `${VAR:-default}` are skipped — the
// deploy-time expander honors the default, so the variable is not
// required and never warrants a missing-var hint. Order matches first
// bare-form appearance in the file. Missing or malformed manifests
// return nil — consistent with loadServiceProtocol's best-effort
// contract.
func extractAgentYamlEnvRefs(projectPath, relativePath string) []string {
	if projectPath == "" || relativePath == "" {
		return nil
	}
	manifestPath := filepath.Join(projectPath, relativePath, "agent.yaml")
	//nolint:gosec // G304: path constructed from azd project root, not user input.
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil
	}
	var hosted agent_yaml.ContainerAgent
	if err := yaml.Unmarshal(data, &hosted); err != nil {
		return nil
	}
	if hosted.EnvironmentVariables == nil {
		return nil
	}

	seen := make(map[string]struct{})
	var out []string
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
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
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
