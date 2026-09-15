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
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
)

type dependencyEnabled func(context.Context, string) (bool, error)

type hostedVoiceTarget struct {
	ServiceName     string
	AgentName       string
	AgentVersion    string
	ProjectEndpoint string
}

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
// as a sibling azd service is wired through uses and references its effective
// resource name, not a service key overridden by the payload. File references are resolved
// against projectRoot without mutating the sibling service configurations.
// References with no local match are external Foundry connection names or IDs
// and are left to the service to resolve.
func validateRegistryConnectionDependency(
	ctx context.Context,
	agent *azdext.ServiceConfig,
	connectionRef string,
	services map[string]*azdext.ServiceConfig,
	projectRoot string,
	isEnabled dependencyEnabled,
) error {
	connectionRef = strings.TrimSpace(connectionRef)
	if connectionRef == "" {
		return nil
	}

	dependency, exists := services[connectionRef]
	if exists && dependency.GetHost() != foundryConnectionHost {
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

	var matches []string
	var overriddenName string
	for key, service := range services {
		if service.GetHost() != foundryConnectionHost {
			continue
		}
		props := ServiceConfigProps(service).AsMap()
		if strings.TrimSpace(projectRoot) == "" && containsFileRef(props) {
			return exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("cannot resolve $ref for connection service %q: project root is empty", key),
				"provide the project directory containing azure.yaml to resolve connection definition references",
			)
		}
		resolved, err := foundry.ResolveFileRefs(props, projectRoot)
		if err != nil {
			return err
		}
		name, _ := resolved["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			name = key
		}
		if key == connectionRef && !strings.EqualFold(name, connectionRef) {
			overriddenName = name
		}
		if key == connectionRef || strings.EqualFold(name, connectionRef) {
			matches = append(matches, key)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	if len(matches) > 1 {
		slices.Sort(matches)
		return exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf("registry connection %q is ambiguous: matches services %q", connectionRef, matches),
			"use unique Foundry connection names and unambiguous service keys for registry connections",
		)
	}
	serviceKey := matches[0]
	if overriddenName != "" {
		return exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf("registryConnectionId %q is a service key whose Connection name is %q", serviceKey, overriddenName),
			fmt.Sprintf("set registryConnectionId to %q and keep %q in the agent uses list", overriddenName, serviceKey),
		)
	}
	if !slices.Contains(agent.GetUses(), serviceKey) {
		return exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf("registry connection service %s is not declared in %s uses",
				strconv.Quote(serviceKey), strconv.Quote(agent.GetName())),
			fmt.Sprintf("add %s to the %s service uses list, run 'azd deploy --all', then retry the agent deployment",
				strconv.Quote(serviceKey), strconv.Quote(agent.GetName())),
		)
	}
	if isEnabled != nil {
		enabled, err := isEnabled(ctx, serviceKey)
		if err != nil {
			return err
		}
		if !enabled {
			return exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf("registry connection service %s is disabled by its deployment condition",
					strconv.Quote(serviceKey)),
				"enable the registry connection dependency or use an external Foundry connection reference",
			)
		}
	}
	return nil
}

// containsFileRef detects references before resolution so an empty project root
// cannot cause a top-level or nested include to be read from the process cwd.
func containsFileRef(value any) bool {
	switch value := value.(type) {
	case map[string]any:
		if _, ok := value["$ref"]; ok {
			return true
		}
		for _, child := range value {
			if containsFileRef(child) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(value, containsFileRef)
	}
	return false
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
			failures = append(failures, newFoundryDependencyFailure(dependencyName, host, detail))
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
			failures = append(failures, foundryDependencyFailure{
				name: toolbox.Name, host: foundryToolboxHost,
				detail:            "toolbox reference has no azure.ai.toolbox service; legacy endpoint markers are not supported",
				requiresMigration: true,
			})
		}
	}

	return foundryDependenciesError(agent, services, failures)
}

func newFoundryDependencyFailure(name, host, detail string) foundryDependencyFailure {
	return foundryDependencyFailure{
		name:              name,
		host:              host,
		detail:            detail,
		requiresProvision: host == foundryProjectHost || host == legacyFoundryHost,
		// Connections, toolboxes, agents, skills and routines are applied during
		// deploy, so their remediation must not send the user to provision.
		requiresDeploy: host == foundryConnectionHost || host == foundryToolboxHost ||
			host == foundryAgentHost || host == foundrySkillHost || host == foundryRoutineHost,
	}
}

func foundryDependenciesError(
	agent *azdext.ServiceConfig,
	services map[string]*azdext.ServiceConfig,
	failures []foundryDependencyFailure,
) error {
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
		actions = append(actions, "declare azure.ai.toolbox services and add them to the agent uses list "+
			"(set endpoint on the toolbox service to reuse an existing toolbox)")
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
	case foundryRoutineHost:
		// A routine names the agent it dispatches, so the dependency edge points
		// from the routine to the agent, not the other way around. The host is
		// listed here so a hand-authored `uses:` entry is recognized rather than
		// falling through to the default; there is nothing to check because the
		// routine extension publishes no readiness marker.
		return ""
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
	serviceProjectKey := envkey.ConnectionServiceProjectEndpoint(service.GetName())
	serviceProject := strings.TrimSpace(env[serviceProjectKey])
	if serviceProject == "" {
		return fmt.Sprintf("%s is not set", serviceProjectKey)
	}
	if !sameProjectEndpoint(serviceProject, env["FOUNDRY_PROJECT_ENDPOINT"]) {
		return fmt.Sprintf("%s does not match FOUNDRY_PROJECT_ENDPOINT", serviceProjectKey)
	}
	return ""
}

func validateFoundryToolboxDependency(service *azdext.ServiceConfig, env map[string]string) string {
	key := envkey.ToolboxMCPEndpoint(service.GetName())
	if strings.TrimSpace(env[key]) == "" {
		return fmt.Sprintf("%s is not set", key)
	}
	if strings.TrimSpace(env["FOUNDRY_PROJECT_ENDPOINT"]) == "" {
		return "FOUNDRY_PROJECT_ENDPOINT is not set"
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

func resolveHostedVoiceTarget(
	wrapper *azdext.ServiceConfig,
	voiceAgentTarget *agent_yaml.VoiceTargetAgent,
	services map[string]*azdext.ServiceConfig,
	env map[string]string,
	projectRoot string,
) (*hostedVoiceTarget, error) {
	if voiceAgentTarget == nil || strings.TrimSpace(voiceAgentTarget.Service) == "" {
		return nil, fmt.Errorf("targetAgent.service is required when modelType is hosted_agent")
	}
	targetServiceName := strings.TrimSpace(voiceAgentTarget.Service)
	if !slices.Contains(wrapper.GetUses(), targetServiceName) {
		return nil, fmt.Errorf(
			"hosted voice target service %q must be declared in the %q service uses list",
			targetServiceName, wrapper.GetName())
	}
	targetService, ok := services[targetServiceName]
	if !ok {
		return nil, fmt.Errorf("hosted voice target service %q was not found in azure.yaml", targetServiceName)
	}
	if targetService.GetHost() != foundryAgentHost {
		return nil, fmt.Errorf(
			"hosted voice target service %q must use host %q, got %q",
			targetServiceName, foundryAgentHost, targetService.GetHost())
	}
	_, isHosted, _, err := LoadAgentDefinition(targetService, projectRoot)
	if err != nil {
		return nil, fmt.Errorf("loading hosted voice target service %q: %w", targetServiceName, err)
	}
	if !isHosted {
		return nil, fmt.Errorf("hosted voice target service %q must have kind hosted", targetServiceName)
	}

	key := normalizeAgentServiceKey(targetServiceName)
	name := strings.TrimSpace(env[fmt.Sprintf("AGENT_%s_NAME", key)])
	version := strings.TrimSpace(env[fmt.Sprintf("AGENT_%s_VERSION", key)])
	projectEndpoint := strings.TrimSpace(env[envkey.AgentProjectEndpoint(targetServiceName)])
	baseEndpoint := strings.TrimSpace(env[fmt.Sprintf("AGENT_%s_ENDPOINT", key)])
	if projectEndpoint == "" && endpointBelongsToProject(baseEndpoint, env["FOUNDRY_PROJECT_ENDPOINT"]) {
		projectEndpoint = strings.TrimRight(strings.TrimSpace(env["FOUNDRY_PROJECT_ENDPOINT"]), "/")
	}
	if name == "" || version == "" || projectEndpoint == "" {
		return nil, fmt.Errorf(
			"hosted voice target service %q is not deployed; run 'azd deploy %s' or 'azd deploy --all'",
			targetServiceName, strconv.Quote(targetServiceName))
	}
	if !sameProjectEndpoint(projectEndpoint, env["FOUNDRY_PROJECT_ENDPOINT"]) {
		return nil, fmt.Errorf(
			"hosted voice target service %q is deployed to a different Foundry project",
			targetServiceName,
		)
	}

	return &hostedVoiceTarget{
		ServiceName:     targetServiceName,
		AgentName:       name,
		AgentVersion:    version,
		ProjectEndpoint: projectEndpoint,
	}, nil
}

func endpointBelongsToProject(resourceEndpoint, projectEndpoint string) bool {
	resourceEndpoint = strings.TrimRight(strings.TrimSpace(resourceEndpoint), "/")
	projectEndpoint = strings.TrimRight(strings.TrimSpace(projectEndpoint), "/")
	return projectEndpoint != "" && strings.HasPrefix(resourceEndpoint, projectEndpoint+"/")
}

func normalizeAgentServiceKey(serviceName string) string {
	return strings.ToUpper(strings.NewReplacer(" ", "_", "-", "_").Replace(serviceName))
}
