// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"net/url"
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
	configurationFix  string
	requiresProvision bool
	requiresDeploy    bool
	requiresMigration bool
}

// validateRegistryConnectionDependency ensures a registry connection declared
// as a sibling azd service is wired through uses. References that do not match a
// local service are external Foundry connection names or IDs and are left to the
// service to resolve.
func validateRegistryConnectionDependency(
	ctx context.Context,
	agent *azdext.ServiceConfig,
	connectionRef string,
	services map[string]*azdext.ServiceConfig,
	isEnabled dependencyEnabled,
) error {
	connectionRef = strings.TrimSpace(connectionRef)
	if connectionRef == "" {
		return nil
	}

	dependency, exists := services[connectionRef]
	if !exists {
		return nil
	}
	if dependency.GetHost() != foundryConnectionHost {
		return exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf(
				"registry connection %s resolves to service host %s instead of %s",
				strconv.Quote(connectionRef),
				strconv.Quote(dependency.GetHost()),
				strconv.Quote(foundryConnectionHost),
			),
			fmt.Sprintf("change the %s service host to %s or use an external Foundry connection reference",
				strconv.Quote(connectionRef), strconv.Quote(foundryConnectionHost)),
		)
	}
	if !slices.Contains(agent.GetUses(), connectionRef) {
		return exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf("registry connection service %s is not declared in %s uses",
				strconv.Quote(connectionRef), strconv.Quote(agent.GetName())),
			fmt.Sprintf("add %s to the %s service uses list, run 'azd provision', then retry the agent deployment",
				strconv.Quote(connectionRef), strconv.Quote(agent.GetName())),
		)
	}
	if isEnabled != nil {
		enabled, err := isEnabled(ctx, connectionRef)
		if err != nil {
			return err
		}
		if !enabled {
			return exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf("registry connection service %s is disabled by its deployment condition",
					strconv.Quote(connectionRef)),
				"enable the registry connection dependency or use an external Foundry connection reference",
			)
		}
	}
	return nil
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
	declared := map[string]struct{}{}
	enabled := map[string]struct{}{}
	disabled := map[string]struct{}{}

	for _, dependencyName := range agent.GetUses() {
		dependency, ok := services[dependencyName]
		if !ok {
			continue
		}
		declared[dependencyName] = struct{}{}
		if isEnabled != nil {
			isDependencyEnabled, err := isEnabled(ctx, dependencyName)
			if err != nil {
				return err
			}
			if !isDependencyEnabled {
				disabled[dependencyName] = struct{}{}
				continue
			}
		}
		enabled[dependencyName] = struct{}{}

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

	if agentConfig != nil {
		for _, toolbox := range agentConfig.Toolboxes {
			service, serviceExists := services[toolbox.Name]
			if _, isDisabled := disabled[toolbox.Name]; isDisabled {
				failures = append(failures, foundryDependencyFailure{
					name: toolbox.Name, host: foundryToolboxHost,
					detail:           "toolbox dependency is disabled by its deployment condition",
					configurationFix: "enable the toolbox dependency or remove it from the agent definition",
					requiresDeploy:   true,
				})
				continue
			}
			_, isEnabled := enabled[toolbox.Name]
			if serviceExists && service.GetHost() != foundryToolboxHost {
				failures = append(failures, foundryDependencyFailure{
					name: toolbox.Name, host: service.GetHost(),
					detail: fmt.Sprintf("toolbox reference resolves to service host %s instead of %s",
						strconv.Quote(service.GetHost()), strconv.Quote(foundryToolboxHost)),
					configurationFix: fmt.Sprintf(
						"change the %s service host to %s or remove it from the agent toolboxes",
						strconv.Quote(toolbox.Name), strconv.Quote(foundryToolboxHost),
					),
				})
				continue
			}
			if serviceExists && isEnabled {
				continue
			}
			_, isDeclared := declared[toolbox.Name]
			if serviceExists && !isDeclared {
				failures = append(failures, foundryDependencyFailure{
					name: toolbox.Name, host: foundryToolboxHost,
					detail: fmt.Sprintf("toolbox service is not declared in %s uses", agent.GetName()),
					configurationFix: fmt.Sprintf(
						"add %s to the %s service uses list",
						strconv.Quote(toolbox.Name), strconv.Quote(agent.GetName()),
					),
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
	configurationFixes := make([]string, 0)
	for _, failure := range failures {
		requiresProvision = requiresProvision || failure.requiresProvision
		requiresDeploy = requiresDeploy || failure.requiresDeploy
		requiresMigration = requiresMigration || failure.requiresMigration
		if failure.configurationFix != "" && !slices.Contains(configurationFixes, failure.configurationFix) {
			configurationFixes = append(configurationFixes, failure.configurationFix)
		}
	}

	actions := slices.Clone(configurationFixes)
	if requiresMigration {
		actions = append(actions, "migrate bundled toolboxes to azure.ai.toolbox services")
	}
	if requiresProvision {
		actions = append(actions, "run 'azd provision'")
	}
	if requiresDeploy && len(configurationFixes) == 0 && !requiresMigration &&
		len(failures) == 1 && serviceExists(services, failures[0].name) {
		actions = append(actions, fmt.Sprintf(
			"run 'azd deploy %s' or 'azd deploy --all', then retry 'azd deploy %s'",
			strconv.Quote(failures[0].name),
			strconv.Quote(agent.GetName()),
		))
	} else if requiresDeploy || requiresMigration {
		actions = append(actions, "run 'azd deploy --all'")
	}
	if len(actions) == 0 {
		actions = append(actions, "run 'azd deploy --all'")
	}
	suggestion := strings.Join(actions, ", then ")
	if !strings.Contains(suggestion, "retry 'azd deploy") {
		suggestion += ", then retry the agent deployment"
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
	projectKey := envkey.SkillProjectEndpoint(service.GetName())
	version := strings.TrimSpace(env[versionKey])
	projectEndpoint := strings.TrimSpace(env[projectKey])
	// Older skill extensions did not publish readiness markers. Preserve those
	// deployments until marker-bearing extension releases can be required.
	if version == "" && projectEndpoint == "" {
		return ""
	}
	if version == "" {
		return fmt.Sprintf("%s is not set", versionKey)
	}
	if !sameProjectEndpoint(env[projectKey], env["FOUNDRY_PROJECT_ENDPOINT"]) {
		return fmt.Sprintf("%s does not match FOUNDRY_PROJECT_ENDPOINT", projectKey)
	}
	return ""
}

func sameProjectEndpoint(a, b string) bool {
	if strings.EqualFold(
		strings.TrimRight(strings.TrimSpace(a), "/"),
		strings.TrimRight(strings.TrimSpace(b), "/"),
	) {
		return true
	}
	aHost, aProject := foundryProjectIdentity(a)
	bHost, bProject := foundryProjectIdentity(b)
	return aHost != "" && aProject != "" &&
		strings.EqualFold(aHost, bHost) && strings.EqualFold(aProject, bProject)
}

func foundryProjectIdentity(endpoint string) (string, string) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Hostname() == "" {
		return "", ""
	}
	const segment = "/projects/"
	index := strings.Index(strings.ToLower(u.Path), segment)
	if index < 0 {
		return "", ""
	}
	project := strings.Split(strings.Trim(u.Path[index+len(segment):], "/"), "/")[0]
	return u.Hostname(), project
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
	connectionProject := strings.TrimSpace(env[envkey.ConnectionProjectEndpoint])
	if connectionProject != "" && !sameProjectEndpoint(connectionProject, env["FOUNDRY_PROJECT_ENDPOINT"]) {
		return fmt.Sprintf("%s does not match FOUNDRY_PROJECT_ENDPOINT", envkey.ConnectionProjectEndpoint)
	}
	found := false
	for name := range strings.SplitSeq(env["AZURE_AI_PROJECT_CONNECTION_NAMES"], ",") {
		if strings.TrimSpace(name) == service.GetName() {
			found = true
			break
		}
	}
	if !found {
		return "connection is not listed in AZURE_AI_PROJECT_CONNECTION_NAMES"
	}
	return ""
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
