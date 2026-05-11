// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
)

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
	authProbe    bool
	openAPIProbe bool
}

// WithAuthProbe enables a token-introspection step that populates
// State.IsAuthenticated. Default false. Only the full-sweep doctor path
// should enable this; every other resolver receives AuthUnknown and
// suppresses login-prompt advice in success paths.
func WithAuthProbe(enabled bool) Option {
	return func(c *config) { c.authProbe = enabled }
}

// WithOpenAPIProbe enables fetching the agent's OpenAPI spec to populate
// State.OpenAPIPayload with a sample invoke payload. Default false. Only
// the `run` command and the doctor full-sweep should enable this.
func WithOpenAPIProbe(enabled bool) Option {
	return func(c *config) { c.openAPIProbe = enabled }
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

	// authProbe and openAPIProbe land in later commits; the flags are
	// already plumbed so call sites and tests can be written against the
	// final API.
	_ = cfg

	return state, errs
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
			IsDeployed:   isDeployed(ctx, src, envName, svc.Name, errs),
		})
	}

	slices.SortFunc(services, func(a, b ServiceState) int {
		return strings.Compare(a.Name, b.Name)
	})
	return services
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
