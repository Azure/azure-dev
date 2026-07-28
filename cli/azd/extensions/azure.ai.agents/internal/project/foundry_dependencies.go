// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type dependencyEnabled func(context.Context, string) (bool, error)

const (
	foundryProjectHost    = "azure.ai.project"
	foundryConnectionHost = "azure.ai.connection"
	foundryToolboxHost    = "azure.ai.toolbox"
	foundryAgentHost      = "azure.ai.agent"
	foundrySkillHost      = "azure.ai.skill"
	foundryRoutineHost    = "azure.ai.routine"
	legacyFoundryHost     = "microsoft.foundry"
)

type foundryDependencyFailure struct {
	name              string
	host              string
	detail            string
	requiresProvision bool
	requiresDeploy    bool
	requiresMigration bool
}

func validateFoundryDependencies(
	ctx context.Context,
	agent *azdext.ServiceConfig,
	agentConfig *ServiceTargetAgentConfig,
	services map[string]*azdext.ServiceConfig,
	env map[string]string,
	isEnabled dependencyEnabled,
) error {
	failures := make([]foundryDependencyFailure, 0)
	visited := map[string]struct{}{agent.GetName(): {}}
	reachable := map[string]struct{}{}
	declared := map[string]struct{}{}
	disabled := map[string]struct{}{}

	var visit func(*azdext.ServiceConfig) error
	visit = func(service *azdext.ServiceConfig) error {
		for _, dependencyName := range service.GetUses() {
			dependency, ok := services[dependencyName]
			if !ok {
				continue
			}
			declared[dependencyName] = struct{}{}
			if isEnabled != nil {
				enabled, err := isEnabled(ctx, dependencyName)
				if err != nil {
					return err
				}
				if !enabled {
					disabled[dependencyName] = struct{}{}
					continue
				}
			}
			reachable[dependencyName] = struct{}{}
			if _, ok := visited[dependencyName]; ok {
				continue
			}
			visited[dependencyName] = struct{}{}

			if err := visit(dependency); err != nil {
				return err
			}

			host := dependency.GetHost()
			detail := validateFoundryDependency(dependency, env)
			if detail != "" {
				failures = append(failures, foundryDependencyFailure{
					name:   dependencyName,
					host:   host,
					detail: detail,
					requiresProvision: host == foundryProjectHost || host == legacyFoundryHost ||
						host == foundryConnectionHost,
					requiresDeploy: host == foundryToolboxHost || host == foundryAgentHost ||
						host == foundrySkillHost,
				})
			}
		}
		return nil
	}
	if err := visit(agent); err != nil {
		return err
	}

	if agentConfig != nil {
		for _, toolbox := range agentConfig.Toolboxes {
			if _, isDisabled := disabled[toolbox.Name]; isDisabled {
				failures = append(failures, foundryDependencyFailure{
					name: toolbox.Name, host: foundryToolboxHost,
					detail: "toolbox dependency is disabled by its deployment condition",
				})
				continue
			}
			_, isReachable := reachable[toolbox.Name]
			if service, ok := services[toolbox.Name]; ok && service.GetHost() == foundryToolboxHost && isReachable {
				continue
			}
			_, isDeclared := declared[toolbox.Name]
			if service, ok := services[toolbox.Name]; ok && service.GetHost() == foundryToolboxHost && !isDeclared {
				failures = append(failures, foundryDependencyFailure{
					name: toolbox.Name, host: foundryToolboxHost,
					detail:         fmt.Sprintf("toolbox service is not declared in %s uses", agent.GetName()),
					requiresDeploy: true,
				})
				continue
			}
			key := envkey.ToolboxMCPEndpoint(toolbox.Name)
			if strings.TrimSpace(env[key]) == "" {
				failures = append(failures, foundryDependencyFailure{
					name:              toolbox.Name,
					host:              foundryToolboxHost,
					detail:            fmt.Sprintf("legacy bundled toolbox has no endpoint in %s", key),
					requiresMigration: true,
				})
				continue
			}
			projectKey := envkey.ToolboxProjectEndpoint(toolbox.Name)
			if strings.TrimSpace(env[projectKey]) != "" {
				if !sameProjectEndpoint(env[projectKey], env["FOUNDRY_PROJECT_ENDPOINT"]) {
					failures = append(failures, foundryDependencyFailure{
						name: toolbox.Name, host: foundryToolboxHost,
						detail:            fmt.Sprintf("legacy bundled toolbox %s does not match FOUNDRY_PROJECT_ENDPOINT", projectKey),
						requiresMigration: true,
					})
				}
			} else if !endpointBelongsToProject(env[key], env["FOUNDRY_PROJECT_ENDPOINT"]) {
				failures = append(failures, foundryDependencyFailure{
					name: toolbox.Name, host: foundryToolboxHost,
					detail:            "legacy bundled toolbox endpoint does not belong to FOUNDRY_PROJECT_ENDPOINT",
					requiresMigration: true,
				})
			}
		}
	}

	if len(failures) == 0 {
		return nil
	}
	slices.SortFunc(failures, func(a, b foundryDependencyFailure) int {
		return strings.Compare(a.name, b.name)
	})

	details := make([]string, len(failures))
	for i, failure := range failures {
		details[i] = fmt.Sprintf("%s (%s): %s", failure.name, failure.host, failure.detail)
	}

	requiresProvision := false
	requiresDeploy := false
	requiresMigration := false
	for _, failure := range failures {
		requiresProvision = requiresProvision || failure.requiresProvision
		requiresDeploy = requiresDeploy || failure.requiresDeploy
		requiresMigration = requiresMigration || failure.requiresMigration
	}

	suggestion := "run 'azd deploy --all', then retry the agent deployment"
	if requiresMigration {
		suggestion = "migrate bundled toolboxes to azure.ai.toolbox services, run 'azd deploy --all', " +
			"then retry the agent deployment"
	} else if requiresProvision && !requiresDeploy {
		suggestion = "run 'azd provision', then retry the agent deployment"
	} else if requiresProvision && requiresDeploy {
		suggestion = "run 'azd provision' and 'azd deploy --all', then retry the agent deployment"
	} else if len(failures) == 1 && serviceExists(services, failures[0].name) {
		suggestion = fmt.Sprintf(
			"run 'azd deploy %s' or 'azd deploy --all', then retry 'azd deploy %s'",
			strconv.Quote(failures[0].name),
			strconv.Quote(agent.GetName()),
		)
	}

	return exterrors.Dependency(
		exterrors.CodeFoundryDependencyNotReady,
		fmt.Sprintf("Foundry dependencies are not ready: %s", strings.Join(details, "; ")),
		suggestion,
	)
}

func validateFoundryDependency(
	service *azdext.ServiceConfig,
	env map[string]string,
) string {
	switch service.GetHost() {
	case foundryProjectHost, legacyFoundryHost:
		return validateFoundryProjectDependency(service, env)
	case foundryConnectionHost:
		return validateFoundryConnectionDependency(service, env)
	case foundryToolboxHost:
		return validateFoundryToolboxDependency(service, env)
	case foundryAgentHost:
		return validateFoundryAgentDependency(service, env)
	case foundrySkillHost:
		return validateFoundrySkillDependency(service, env)
	default:
		return ""
	}
}

func validateFoundrySkillDependency(service *azdext.ServiceConfig, env map[string]string) string {
	versionKey := envkey.SkillVersion(service.GetName())
	if strings.TrimSpace(env[versionKey]) == "" {
		return fmt.Sprintf("%s is not set", versionKey)
	}
	projectKey := envkey.SkillProjectEndpoint(service.GetName())
	if strings.TrimRight(strings.TrimSpace(env[projectKey]), "/") !=
		strings.TrimRight(strings.TrimSpace(env["FOUNDRY_PROJECT_ENDPOINT"]), "/") {
		return fmt.Sprintf("%s does not match FOUNDRY_PROJECT_ENDPOINT", projectKey)
	}
	return ""
}

func sameProjectEndpoint(a, b string) bool {
	return strings.EqualFold(
		strings.TrimRight(strings.TrimSpace(a), "/"),
		strings.TrimRight(strings.TrimSpace(b), "/"),
	)
}

func serviceExists(services map[string]*azdext.ServiceConfig, name string) bool {
	_, ok := services[name]
	return ok
}

func validateFoundryProjectDependency(_ *azdext.ServiceConfig, env map[string]string) string {
	if strings.TrimSpace(env["FOUNDRY_PROJECT_ENDPOINT"]) == "" {
		return "FOUNDRY_PROJECT_ENDPOINT is not set"
	}
	return ""
}

func validateFoundryConnectionDependency(service *azdext.ServiceConfig, env map[string]string) string {
	if !sameProjectEndpoint(env[envkey.ConnectionProjectEndpoint], env["FOUNDRY_PROJECT_ENDPOINT"]) {
		return fmt.Sprintf("%s does not match FOUNDRY_PROJECT_ENDPOINT", envkey.ConnectionProjectEndpoint)
	}
	for name := range strings.SplitSeq(env["AZURE_AI_PROJECT_CONNECTION_NAMES"], ",") {
		if strings.TrimSpace(name) == service.GetName() {
			return ""
		}
	}
	return "connection is not listed in AZURE_AI_PROJECT_CONNECTION_NAMES"
}

func validateFoundryToolboxDependency(service *azdext.ServiceConfig, env map[string]string) string {
	key := envkey.ToolboxMCPEndpoint(service.GetName())
	if strings.TrimSpace(env[key]) == "" {
		return fmt.Sprintf("%s is not set", key)
	}
	projectKey := envkey.ToolboxProjectEndpoint(service.GetName())
	if strings.TrimSpace(env[projectKey]) == "" && endpointBelongsToProject(env[key], env["FOUNDRY_PROJECT_ENDPOINT"]) {
		return ""
	}
	if !sameProjectEndpoint(env[projectKey], env["FOUNDRY_PROJECT_ENDPOINT"]) {
		return fmt.Sprintf("%s does not match FOUNDRY_PROJECT_ENDPOINT", projectKey)
	}
	return ""
}

func validateFoundryAgentDependency(service *azdext.ServiceConfig, env map[string]string) string {
	key := normalizeAgentServiceKey(service.GetName())
	missing := make([]string, 0, 2)
	for _, suffix := range []string{"NAME", "VERSION"} {
		envKey := fmt.Sprintf("AGENT_%s_%s", key, suffix)
		if strings.TrimSpace(env[envKey]) == "" {
			missing = append(missing, envKey)
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf("%s not set", strings.Join(missing, " and "))
	}
	projectKey := envkey.AgentProjectEndpoint(service.GetName())
	baseEndpointKey := fmt.Sprintf("AGENT_%s_ENDPOINT", key)
	if strings.TrimSpace(env[projectKey]) == "" && endpointBelongsToProject(
		env[baseEndpointKey], env["FOUNDRY_PROJECT_ENDPOINT"],
	) {
		return ""
	}
	if !sameProjectEndpoint(env[projectKey], env["FOUNDRY_PROJECT_ENDPOINT"]) {
		return fmt.Sprintf("%s does not match FOUNDRY_PROJECT_ENDPOINT", projectKey)
	}
	return ""
}

func endpointBelongsToProject(resourceEndpoint, projectEndpoint string) bool {
	resourceEndpoint = strings.TrimRight(strings.TrimSpace(resourceEndpoint), "/")
	projectEndpoint = strings.TrimRight(strings.TrimSpace(projectEndpoint), "/")
	return projectEndpoint != "" && strings.HasPrefix(resourceEndpoint, projectEndpoint+"/")
}

func normalizeAgentServiceKey(serviceName string) string {
	return strings.ToUpper(strings.NewReplacer(" ", "_", "-", "_").Replace(serviceName))
}
