// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/cmd/nextstep"
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/agentkind"
	"azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/github"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

type azureYamlManifestInfo struct {
	hasServices       bool
	hasAgentService   bool
	hasPromptAgent    bool
	hasNonPromptAgent bool
	hasUnresolvedRefs bool
}

func (i azureYamlManifestInfo) promptOnly() bool {
	return i.hasPromptAgent && !i.hasNonPromptAgent && !i.hasUnresolvedRefs
}

// inspectAzureYaml identifies unified manifests and Agent services.
//
// Local references are resolved against projectRoot. Remote references
// wait for the sample directory download.
func inspectAzureYaml(content []byte, projectRoot string) (azureYamlManifestInfo, error) {
	var info azureYamlManifestInfo
	var top map[string]any
	if err := yaml.Unmarshal(content, &top); err != nil {
		return info, nil
	}

	services, ok := top["services"].(map[string]any)
	if !ok {
		return info, nil
	}
	info.hasServices = true

	for serviceName, svc := range services {
		svcMap, ok := svc.(map[string]any)
		if !ok {
			continue
		}

		if hasAzureYamlFileRef(svcMap) {
			if projectRoot == "" {
				info.hasUnresolvedRefs = true
			} else {
				resolved, err := foundry.ResolveFileRefs(svcMap, projectRoot)
				if err != nil {
					return info, fmt.Errorf(
						"resolving $ref includes for service %q: %w",
						serviceName,
						err,
					)
				}
				svcMap = resolved
			}
		}

		host, _ := svcMap["host"].(string)
		if host == AiAgentHost {
			info.hasAgentService = true
			kind, _ := svcMap["kind"].(string)
			if strings.EqualFold(strings.TrimSpace(kind), string(agent_yaml.AgentKindPrompt)) {
				info.hasPromptAgent = true
			} else {
				info.hasNonPromptAgent = true
			}
		}
	}

	return info, nil
}

func hasAzureYamlFileRef(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if _, ok := typed["$ref"]; ok {
			return true
		}
		for _, child := range typed {
			if hasAzureYamlFileRef(child) {
				return true
			}
		}
	case []any:
		if slices.ContainsFunc(typed, hasAzureYamlFileRef) {
			return true
		}
	}

	return false
}

func missingAgentServiceError(manifestPointer string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidManifestPointer,
		fmt.Sprintf(
			"manifest %q is a unified azure.yaml but does not declare an agent service",
			manifestPointer,
		),
		fmt.Sprintf(
			"add a service with host: %s, or pass an agent manifest",
			AiAgentHost,
		),
	)
}

func validateStagedAzureYaml(stagingDir, manifestPointer string) error {
	manifestPath := filepath.Join(stagingDir, "azure.yaml")
	//nolint:gosec // stagingDir is created or selected by the init flow
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("reading staged azure.yaml: %w", err)
	}

	info, err := inspectAzureYaml(content, stagingDir)
	if err != nil {
		return err
	}
	if !info.hasServices || !info.hasAgentService {
		return missingAgentServiceError(manifestPointer)
	}

	return nil
}

// foundryProjectName returns the top-level `name:` of a unified azure.yaml, used
// to derive the project folder name. Returns "" when the name is absent or the
// content cannot be parsed.
func foundryProjectName(content []byte) string {
	var top map[string]any
	if err := yaml.Unmarshal(content, &top); err != nil {
		return ""
	}
	if name, ok := top["name"].(string); ok {
		return strings.TrimSpace(name)
	}
	return ""
}

type adoptedAgentNameResolver func(context.Context, string) (string, error)

func adoptedAgentNameConflictSuggestion() string {
	return "To create a separate agent, re-run init with --agent-name <unique-name> for single-agent samples, " +
		"or update each agent service's `name` in the adopted azure.yaml.\n"
}

// confirmAdoptedAgentNameConflicts checks every agent definition embedded in an
// adopted azure.yaml against the selected existing Foundry project. The shared
// resolver warns and asks for confirmation before reusing a name, or prompts
// for a replacement name and returns it.
func confirmAdoptedAgentNameConflicts(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	environment *azdext.Environment,
	credential azcore.TokenCredential,
	noPrompt bool,
) error {
	return updateAdoptedAgentNames(ctx, azdClient, func(ctx context.Context, agentName string) (string, error) {
		return resolveExistingAgentNameConflict(
			ctx,
			azdClient,
			environment,
			credential,
			noPrompt,
			agentName,
			withNoPromptAgentNameConflictSuggestion(adoptedAgentNameConflictSuggestion()),
		)
	})
}

// updateAdoptedAgentNames resolves name conflicts for adopted agent services
// and persists any replacement names in azure.yaml.
func updateAdoptedAgentNames(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	resolveName adoptedAgentNameResolver,
) error {
	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("reading adopted project for agent name conflicts: %w", err)
	}

	services := resp.GetProject().GetServices()
	serviceNames := make([]string, 0, len(services))
	for serviceName, svc := range services {
		if svc.GetHost() == AiAgentHost {
			serviceNames = append(serviceNames, serviceName)
		}
	}
	slices.Sort(serviceNames)

	for _, serviceName := range serviceNames {
		agentName, configPath := adoptedAgentNameConfig(services[serviceName])
		if agentName == "" {
			continue
		}

		resolvedName, err := resolveName(ctx, agentName)
		if err != nil {
			return err
		}
		if resolvedName == agentName {
			continue
		}

		value, err := structpb.NewValue(resolvedName)
		if err != nil {
			return fmt.Errorf("encoding replacement name for agent service %q: %w", serviceName, err)
		}
		if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
			ServiceName: serviceName,
			Path:        configPath,
			Value:       value,
		}); err != nil {
			return fmt.Errorf("updating agent name in azure.yaml for service %q: %w", serviceName, err)
		}
	}

	return nil
}

func applyAdoptedAgentNameOverride(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	agentName string,
) error {
	agentName, err := validateInitAgentName(agentName)
	if err != nil {
		return err
	}

	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("reading adopted project for agent name override: %w", err)
	}

	var serviceName string
	var configPath string
	for name, svc := range resp.GetProject().GetServices() {
		if svc.GetHost() != AiAgentHost {
			continue
		}
		if serviceName != "" {
			return exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--agent-name cannot be applied to an adopted azure.yaml with multiple agent services",
				"update each agent service's `name` in azure.yaml after init, or use a sample with a single agent service",
			)
		}
		serviceName = name
		configPath = adoptedAgentNameOverrideConfigPath(svc)
	}
	if serviceName == "" {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--agent-name could not be applied because the adopted azure.yaml has no agent service",
			"update the agent service's `name` in azure.yaml after init, or use a sample with an agent service",
		)
	}

	value, err := structpb.NewValue(agentName)
	if err != nil {
		return fmt.Errorf("encoding agent name override for agent service %q: %w", serviceName, err)
	}
	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        configPath,
		Value:       value,
	}); err != nil {
		return fmt.Errorf("updating agent name in adopted azure.yaml for service %q: %w", serviceName, err)
	}

	return nil
}

func adoptedAgentNameOverrideConfigPath(svc *azdext.ServiceConfig) string {
	if svc == nil {
		return "name"
	}
	if legacy := svc.GetConfig(); legacy != nil && legacy.GetFields()["kind"].GetStringValue() != "" {
		return "config.name"
	}
	return "name"
}

// adoptedAgentNameConfig returns the Foundry agent name and its service-relative
// config path for the unified inline shape or deprecated config-nested shape.
func adoptedAgentNameConfig(svc *azdext.ServiceConfig) (string, string) {
	if svc == nil {
		return "", ""
	}

	inline := svc.GetAdditionalProperties()
	if inline != nil && inline.GetFields()["kind"].GetStringValue() != "" {
		return strings.TrimSpace(inline.GetFields()["name"].GetStringValue()), "name"
	}

	legacy := svc.GetConfig()
	if legacy != nil && legacy.GetFields()["kind"].GetStringValue() != "" {
		return strings.TrimSpace(legacy.GetFields()["name"].GetStringValue()), "config.name"
	}

	return "", ""
}

// readManifestContentForInitDetection returns the pointed-at YAML content for
// init-mode routing. It first uses the cheap peek path; when that cannot read a
// GitHub URL (for example, a private repository), it falls back to the
// authenticated GitHub CLI download path so private unified azure.yaml samples
// can still be classified and adopted.
func readManifestContentForInitDetection(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	manifestPointer string,
	httpClient *http.Client,
) ([]byte, bool) {
	if content, ok := readManifestContentForPeek(ctx, manifestPointer, httpClient); ok {
		return content, true
	}
	cachedContent, cached := readCachedTemplateManifest(manifestPointer)
	if cached {
		return cachedContent, true
	}
	if templateCacheRoot() != "" {
		return nil, false
	}
	if azdClient == nil || !strings.Contains(manifestPointer, "://") {
		return nil, false
	}

	parsedURL, err := url.Parse(manifestPointer)
	if err != nil || !strings.Contains(parsedURL.Hostname(), "github") {
		return nil, false
	}

	commandRunner := exec.NewCommandRunner(&exec.RunnerOptions{
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	console := input.NewConsole(
		false, // noPrompt
		true,  // isTerminal
		input.Writers{Output: io.Discard},
		input.ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
		},
		nil, // formatter
		nil, // externalPromptCfg
	)
	ghCli := github.NewGitHubCli(console, commandRunner)
	if err := ghCli.EnsureInstalled(ctx); err != nil {
		log.Printf("detect unified azure.yaml: ensuring gh is installed: %v", err)
		return nil, false
	}

	urlInfo, err := parseGitHubUrlForAdopt(ctx, azdClient, manifestPointer)
	if err != nil {
		log.Printf("detect unified azure.yaml: parsing GitHub URL: %v", err)
		return nil, false
	}

	apiPath := fmt.Sprintf("/repos/%s/contents/%s", urlInfo.RepoSlug, urlInfo.FilePath)
	if urlInfo.Branch != "" {
		apiPath += fmt.Sprintf("?ref=%s", urlInfo.Branch)
	}
	content, err := downloadGithubManifest(ctx, urlInfo, apiPath, ghCli)
	if err != nil {
		log.Printf("detect unified azure.yaml: downloading GitHub file: %v", err)
		return nil, false
	}

	return []byte(content), true
}

// runInitFromAzureYaml adopts a sample's unified Foundry `azure.yaml` as the
// project-root manifest instead of generating one from an agent manifest
// (#8798). The sample's `azure.yaml` and the files it references are placed at
// the project root via azd-core's native template adoption; the services it
// already declares (project, connections, toolboxes, agents) are not
// re-derived. `content` is the already-fetched azure.yaml used to derive the
// project folder name.
func runInitFromAzureYaml(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
	content []byte,
) error {
	projectName := foundryProjectName(content)
	agentNameOverride, err := adoptedAgentNameOverride(flags)
	if err != nil {
		return err
	}
	if agentNameOverride != "" {
		projectName = agentNameOverride
	}

	targetDir, folderDisplay := adoptTargetDir(flags, projectName)

	// Adoption is a fresh-project operation: it lays down the project-root
	// azure.yaml. When the target already contains an azd project manifest we
	// cannot adopt over it; merging the sample's services into an existing
	// azure.yaml is tracked separately (#8884).
	if projectManifestExists(targetDir) {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			fmt.Sprintf("a project azure.yaml already exists in %q, so the sample's "+
				"unified azure.yaml cannot be adopted there", targetDir),
			"run this command in an empty directory (or pass a new target directory) to "+
				"adopt the sample, or add an individual agent to this project with "+
				"'azd ai agent init -m <agent.manifest.yaml>'",
		)
	}
	// Stage the sample as a local template directory (azure.yaml at its root
	// alongside referenced files) that azd-core can adopt with `azd init -t`.
	stagingDir, cleanup, err := stageAzureYamlTemplate(ctx, flags, azdClient, httpClient)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := validateStagedAzureYaml(stagingDir, flags.manifestPointer); err != nil {
		return err
	}
	stagedContent, err := os.ReadFile(filepath.Join(stagingDir, "azure.yaml"))
	if err != nil {
		return fmt.Errorf("reading staged azure.yaml: %w", err)
	}
	stagedInfo, err := inspectAzureYaml(stagedContent, stagingDir)
	if err != nil {
		return err
	}
	promptOnly := stagedInfo.promptOnly()
	if agentNameOverride != "" {
		// Validate against the fully staged template so services whose host lives
		// inside a local $ref are counted the same way azd-core will load them.
		if err := validateAdoptedAgentNameOverride(stagedContent, stagingDir); err != nil {
			return err
		}
	}

	fmt.Println(output.WithGrayFormat("Adopting the sample's azure.yaml as your project manifest..."))

	envName := deriveEnvName(flags, targetDir)
	if err := scaffoldProject(ctx, azdClient, targetDir, stagingDir, envName); err != nil {
		return err
	}

	// Defensive: the sample should already declare `infra.provider:
	// microsoft.foundry`, but stamp it if missing so provisioning stays
	// bicep-less by default.
	if err := ensureFoundryProviderDeclared(ctx, azdClient); err != nil {
		return err
	}
	if agentNameOverride != "" {
		if err := applyAdoptedAgentNameOverride(ctx, azdClient, agentNameOverride); err != nil {
			return err
		}
	}

	// --- Interactive Azure context setup (subscription, Foundry project) ---
	// The scaffolding created an environment; load it and run the same Foundry
	// project selection flow as the agent-manifest path so the user ends up
	// with a provision-ready environment.
	env := getExistingEnvironment(ctx, envName, azdClient)
	if env == nil {
		// Environment should exist after scaffoldProject; if not, create one.
		env, err = createNewEnvironment(ctx, azdClient, envName)
		if err != nil {
			return err
		}
	}

	azureContext, err := loadAzureContext(ctx, azdClient, env.Name)
	if err != nil {
		return err
	}

	// Apply deploy-mode configuration to the adopted agent
	// service(s) before configuring the Foundry project. Whether an
	// Azure Container Registry must be wired (skipACR) depends on the
	// resolved deploy mode: a container agent on an existing project
	// needs AZURE_CONTAINER_REGISTRY_ENDPOINT set here, while a code
	// agent (or a user-supplied --image) does not.
	projectNeedsACR := false
	var configuredSourceContainers []string
	if !promptOnly {
		projectNeedsACR, configuredSourceContainers, err = applyDeployModeToAdoptedProjectWithSources(
			ctx, flags, azdClient,
		)
		if err != nil {
			return err
		}
	}

	// Only source-container deploys require an ACR. Code deploy and pre-built
	// images skip it.
	skipACR := !projectNeedsACR
	// Hosted-region filtering is independent from ACR setup. Prompt agents are
	// managed by Foundry and must not inherit hosted-agent region constraints.
	filterHostedRegions := true
	projectRoot := resolveProjectPath(ctx, azdClient)
	environmentValues, err := getAgentEnvironmentValues(ctx, azdClient, env.Name)
	if err != nil {
		return fmt.Errorf("reading environment values: %w", err)
	}
	preserveDeferredProjectState, err := projectServiceHasEndpoint(
		ctx,
		azdClient,
		projectRoot,
		environmentValues,
	)
	if err != nil {
		return err
	}
	deferredAzureContext := shouldDeferAdoptedModelAuthoring(
		flags, azureContext,
	)

	result, err := configureFoundryProject(
		ctx, azdClient, azureContext, env.Name,
		flags.projectResourceId, flags.acrConnection, flags.noPrompt,
		skipACR,
		filterHostedRegions && !promptOnly,
		preserveDeferredProjectState,
	)
	if err != nil {
		if exterrors.IsCancellation(err) {
			return exterrors.Cancelled("initialization was cancelled")
		}
		return err
	}
	if deferredAzureContext && strings.TrimSpace(flags.model) != "" {
		fmt.Printf("%s", output.WithWarningFormat(
			"Model configuration was deferred because Azure environment values are missing.\n",
		))
		fmt.Println(output.WithGrayFormat(
			"Set the missing values, then re-run init to author the model deployment.",
		))
	}
	if err := validateAdoptedModelDeploymentTarget(
		flags,
		result.FoundryProject,
	); err != nil {
		return err
	}
	projectRoot, err = os.Getwd()
	if err != nil {
		return fmt.Errorf(
			"resolving the adopted project directory: %w",
			err,
		)
	}

	// When an existing project was selected, record its endpoint in the azd
	// environment, then let the projects extension reconcile the project
	// service. Agents preserve that service but do not author its shape.
	if result.FoundryProject != nil {
		if err := recordFoundryProjectEnv(
			ctx,
			azdClient,
			env.Name,
			result.FoundryProject,
		); err != nil {
			return err
		}
		if err := authorSelectedFoundryProject(
			ctx,
			azdClient,
			env.Name,
			result.FoundryProject,
			projectRoot,
			projectAuthoringExisting,
			flags.noPrompt,
		); err != nil {
			return err
		}
		if err := finalizeAdoptedSourceContainerNetwork(
			ctx,
			azdClient,
			configuredSourceContainers,
			result.FoundryProject.NetworkInjected,
		); err != nil {
			return err
		}
		projectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
		if err != nil {
			return fmt.Errorf("reading adopted project registry connections: %w", err)
		}
		externalConnections, err := adoptedExternalRegistryConnections(
			projectResponse.GetProject(), flags.registryConnection,
		)
		if err != nil {
			return err
		}
		for _, connectionRef := range externalConnections {
			if err := verifyRegistryConnectionOnProject(
				ctx,
				result.Credential,
				*result.FoundryProject,
				connectionRef,
			); err != nil {
				return err
			}
		}
		if err := confirmAdoptedAgentNameConflicts(
			ctx,
			azdClient,
			env,
			result.Credential,
			flags.noPrompt,
		); err != nil {
			return err
		}
	}
	if result.FoundryProject == nil {
		if err := authorSelectedFoundryProject(
			ctx,
			azdClient,
			env.Name,
			nil,
			projectRoot,
			result.AuthoringMode,
			flags.noPrompt,
		); err != nil {
			return err
		}
	}
	if err := wireAdoptedProjectDependency(ctx, azdClient); err != nil {
		return err
	}

	// The projects extension owns project deployments. When the user
	// names a model, delegate its authoring. Existing deployment lookup
	// remains limited to --model-deployment.
	if !deferredAzureContext {
		if err := configureAdoptedModel(
			ctx,
			azdClient,
			projectRoot,
			flags,
		); err != nil {
			return err
		}
	}
	if result.FoundryProject != nil && result.Credential != nil {
		if err := configureAdoptedExistingDeployment(
			ctx,
			azdClient,
			env.Name,
			result.Credential,
			result.FoundryProject,
			flags,
		); err != nil {
			if exterrors.IsCancellation(err) {
				return exterrors.Cancelled("initialization was cancelled")
			}
			return err
		}
	}

	// scaffoldProject changes the extension process into the adopted project root.
	if err := configureAzureYamlEnvironmentVariables(
		ctx,
		azdClient,
		env.Name,
		".",
		flags.noPrompt,
	); err != nil {
		return err
	}

	fmt.Printf(
		"\nAdopted the sample's azure.yaml as the project manifest at %s.\n",
		output.WithHighLightFormat("azure.yaml"),
	)

	printAdoptionNextSteps(ctx, azdClient, folderDisplay, promptOnly)
	return nil
}

func wireAdoptedProjectDependency(
	ctx context.Context,
	azdClient *azdext.AzdClient,
) error {
	projectServiceName, err := resolveProjectServiceKey(ctx, azdClient)
	if err != nil {
		return err
	}
	response, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("reading adopted agent services: %w", err)
	}
	if response.GetProject() == nil {
		return missingServiceDependencyError(projectServiceName, AiProjectHost)
	}
	services := response.GetProject().GetServices()
	for _, name := range slices.Sorted(maps.Keys(services)) {
		if services[name].GetHost() != AiAgentHost {
			continue
		}
		if _, err := addAgentServiceDependency(
			ctx, azdClient, name, projectServiceName, "project", AiProjectHost,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateAdoptedModelDeploymentTarget(
	flags *initFlags,
	projectInfo *FoundryProjectInfo,
) error {
	if strings.TrimSpace(flags.modelDeployment) == "" ||
		projectInfo != nil {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeConflictingArguments,
		"--model-deployment requires an existing Foundry project",
		"select an existing project or use --model to deploy a new model",
	)
}

func configureAdoptedModel(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	projectRoot string,
	flags *initFlags,
) error {
	if strings.TrimSpace(flags.modelDeployment) != "" {
		return nil
	}
	model := strings.TrimSpace(flags.model)
	if model == "" {
		return nil
	}
	return authorFoundryDeployments(
		ctx,
		azdClient,
		projectRoot,
		[]project.Deployment{
			{Model: project.DeploymentModel{Name: model}},
		},
	)
}

func shouldDeferAdoptedModelAuthoring(
	flags *initFlags,
	azureContext *azdext.AzureContext,
) bool {
	return flags.projectResourceId == "" &&
		shouldDeferInitAzureContext(flags.noPrompt, azureContext)
}

func configureAdoptedExistingDeployment(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	credential azcore.TokenCredential,
	projectInfo *FoundryProjectInfo,
	flags *initFlags,
) error {
	requested := strings.TrimSpace(flags.modelDeployment)
	if requested == "" {
		return nil
	}

	deployments, err := listProjectDeployments(
		ctx,
		credential,
		projectInfo.SubscriptionId,
		projectInfo.ResourceGroupName,
		projectInfo.AccountName,
	)
	if err != nil {
		return fmt.Errorf("listing model deployments for Foundry project: %w", err)
	}

	var selected *FoundryDeploymentInfo
	for i := range deployments {
		deployment := &deployments[i]
		if requested != "" &&
			strings.EqualFold(deployment.Name, requested) {
			selected = deployment
			break
		}
	}
	if selected == nil {
		return exterrors.Validation(
			exterrors.CodeModelDeploymentNotFound,
			fmt.Sprintf(
				"model deployment %q not found in Foundry project",
				requested,
			),
			"verify the deployment name or omit --model-deployment",
		)
	}
	if err := setEnvValue(
		ctx,
		azdClient,
		envName,
		"AZURE_AI_MODEL_DEPLOYMENT_NAME",
		selected.Name,
	); err != nil {
		return fmt.Errorf(
			"storing AZURE_AI_MODEL_DEPLOYMENT_NAME: %w",
			err,
		)
	}
	if err := updatePendingModelDeploymentSignal(
		ctx,
		azdClient,
		envName,
		true,
		false,
	); err != nil {
		log.Printf(
			"warning: failed to update model_deployment provision signal: %v",
			err,
		)
	}
	return nil
}

func validateAdoptedAgentNameOverride(content []byte, projectRoot string) error {
	var doc map[string]any
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return fmt.Errorf("parsing adopted azure.yaml for agent name override: %w", err)
	}

	services, ok := doc["services"].(map[string]any)
	if !ok {
		services = map[string]any{}
	}

	agentServices := 0
	for serviceName, svc := range services {
		svcMap, ok := svc.(map[string]any)
		if !ok {
			continue
		}
		if hasAzureYamlFileRef(svcMap) && projectRoot != "" {
			resolved, err := foundry.ResolveFileRefs(svcMap, projectRoot)
			if err != nil {
				return fmt.Errorf("resolving $ref includes for service %q: %w", serviceName, err)
			}
			svcMap = resolved
		}

		host, _ := svcMap["host"].(string)
		if host != AiAgentHost {
			continue
		}
		agentServices++
		if agentServices > 1 {
			return exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"--agent-name cannot be applied to an adopted azure.yaml with multiple agent services",
				"update each agent service's `name` in azure.yaml after init, or use a sample with a single agent service",
			)
		}
	}
	if agentServices == 0 {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--agent-name could not be applied because the adopted azure.yaml has no agent service",
			"update the agent service's `name` in azure.yaml after init, or use a sample with an agent service",
		)
	}

	return nil
}

func adoptedAgentNameOverride(flags *initFlags) (string, error) {
	if !flags.agentNameExplicit {
		return "", nil
	}
	agentNameOverride := strings.TrimSpace(flags.agentName)
	if agentNameOverride == "" {
		return "", nil
	}
	validatedName, err := validateInitAgentName(agentNameOverride)
	if err != nil {
		return "", err
	}
	flags.agentName = validatedName
	return validatedName, nil
}

// adoptTargetDir resolves the directory the adopted project is created in and
// the display path for the "created folder" next-step hint. An explicit --src
// (or positional directory) wins; otherwise a new folder named after the
// sample's project name is used, falling back to the current directory when the
// sample has no name.
func adoptTargetDir(flags *initFlags, projectName string) (targetDir string, folderDisplay string) {
	if flags.src != "" {
		return flags.src, folderDisplayIfNew(flags.src)
	}
	if projectName == "" {
		return ".", ""
	}
	folder := sanitizeAgentName(projectName)
	if folder == "" {
		return ".", ""
	}
	return folder, folderDisplayIfNew(folder)
}

// folderDisplayIfNew returns a slash-formatted display path when dir does not
// yet exist (so the cd hint is only shown for newly-created folders), else "".
func folderDisplayIfNew(dir string) string {
	if dir == "." {
		return ""
	}
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return filepath.ToSlash(dir)
	}
	return ""
}

func projectManifestExists(dir string) bool {
	return fileExists(filepath.Join(dir, "azure.yaml")) ||
		fileExists(filepath.Join(dir, "azure.yml"))
}

// stageAzureYamlTemplate produces a local directory that azd-core can adopt as a
// template (`azd init -t <dir>`): it contains the sample's azure.yaml at its
// root alongside the sibling files/dirs the manifest references.
//
// For a local pointer the pointer's parent directory is used directly when the
// file is already named azure.yaml(.yml); otherwise a temp copy of the
// directory is staged with the manifest written as azure.yaml. For a remote
// GitHub pointer the azure.yaml's containing directory is downloaded into a temp
// staging dir. The returned cleanup removes any temp directory created.
func stageAzureYamlTemplate(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
) (string, func(), error) {
	noop := func() {}
	pointer := flags.manifestPointer

	if isLocalFilePath(pointer) {
		dir := filepath.Dir(pointer)
		base := strings.ToLower(filepath.Base(pointer))
		if base == "azure.yaml" {
			return dir, noop, nil
		}

		// The pointer file isn't named azure.yaml: stage a temp copy of the
		// directory and write the manifest as azure.yaml so azd-core adopts it.
		staging, err := os.MkdirTemp("", "azd-foundry-adopt-*")
		if err != nil {
			return "", noop, fmt.Errorf("creating staging dir: %w", err)
		}
		cleanup := func() { _ = os.RemoveAll(staging) }
		// Staging is all-or-nothing: without azure.yaml at the template root,
		// azd-core would generate a default manifest instead of adopting this
		// sample, so every error path removes the partial copy.
		if err := copyDirectory(dir, staging); err != nil {
			cleanup()
			return "", noop, fmt.Errorf("staging sample directory: %w", err)
		}
		//nolint:gosec // manifest path is an explicit user-provided local path
		data, err := os.ReadFile(pointer)
		if err != nil {
			cleanup()
			return "", noop, fmt.Errorf("reading sample azure.yaml: %w", err)
		}
		//nolint:gosec // staging dir is from os.MkdirTemp and the filename is a constant
		if err := os.WriteFile(filepath.Join(staging, "azure.yaml"), data, osutil.PermissionFile); err != nil {
			cleanup()
			return "", noop, fmt.Errorf("writing staged azure.yaml: %w", err)
		}
		if err := os.Remove(filepath.Join(staging, filepath.Base(pointer))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			cleanup()
			return "", noop, fmt.Errorf("removing staged source manifest: %w", err)
		}
		return staging, cleanup, nil
	}

	// Remote GitHub pointer: download the directory containing the azure.yaml.
	staging, err := os.MkdirTemp("", "azd-foundry-adopt-*")
	if err != nil {
		return "", noop, fmt.Errorf("creating staging dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(staging) }
	if err := stageRemoteAzureYaml(ctx, azdClient, httpClient, pointer, staging); err != nil {
		cleanup()
		return "", noop, err
	}
	return staging, cleanup, nil
}

// ensureStagedAzureYaml normalizes a staged template so azd-core sees
// azure.yaml at the template root. azd-core only adopts azure.yaml; if a sample
// ships azure.yml, copy it to azure.yaml and remove the alias to avoid leaving
// duplicate project manifests in the initialized project.
func ensureStagedAzureYaml(staging string) (bool, error) {
	azureYaml := filepath.Join(staging, "azure.yaml")
	if fileExists(azureYaml) {
		return true, nil
	}

	azureYml := filepath.Join(staging, "azure.yml")
	if !fileExists(azureYml) {
		return false, nil
	}

	//nolint:gosec // azure.yml is in a temp staging dir produced by this command
	data, err := os.ReadFile(azureYml)
	if err != nil {
		return false, fmt.Errorf("reading staged azure.yml: %w", err)
	}
	//nolint:gosec // staging dir is from os.MkdirTemp and the filename is a constant
	if err := os.WriteFile(azureYaml, data, osutil.PermissionFile); err != nil {
		return false, fmt.Errorf("writing staged azure.yaml: %w", err)
	}
	if err := os.Remove(azureYml); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("removing staged azure.yml: %w", err)
	}
	return true, nil
}

func clearStagingDirectory(staging string) error {
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("clearing staging directory: %w", err)
	}
	if err := os.MkdirAll(staging, osutil.PermissionDirectory); err != nil {
		return fmt.Errorf("recreating staging directory: %w", err)
	}
	return nil
}

// stageRemoteAzureYaml downloads the directory containing the remote azure.yaml
// into staging. It first tries an unauthenticated public download (no gh CLI),
// then falls back to the GitHub CLI for private repositories or URL forms the
// naive parser can't handle — mirroring downloadAgentYaml's resolution order.
func stageRemoteAzureYaml(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
	pointer string,
	staging string,
) error {
	fmt.Println(output.WithGrayFormat("Downloading sample from GitHub..."))

	triedPublicDownload := false
	var publicDownloadErr error
	if urlInfo := parseGitHubUrlNaive(pointer); urlInfo != nil {
		triedPublicDownload = true
		dirPath := parentDirOf(urlInfo.FilePath)
		err := downloadDirectoryContentsWithoutGhCli(
			ctx, urlInfo.RepoSlug, dirPath, dirPath, urlInfo.Branch, staging, httpClient,
		)
		if err == nil {
			hasAzureYaml, normalizeErr := ensureStagedAzureYaml(staging)
			if normalizeErr != nil {
				return normalizeErr
			}
			if hasAzureYaml {
				if cacheErr := refreshTemplateCache(pointer, staging); cacheErr != nil {
					emitTemplateCacheWarning(fmt.Sprintf("Unable to refresh the sample cache: %s", cacheErr))
				}
				return nil
			}
			publicDownloadErr = errors.New("downloaded sample did not contain azure.yaml")
		} else {
			publicDownloadErr = err
		}
	}

	if triedPublicDownload {
		if err := clearStagingDirectory(staging); err != nil {
			return err
		}
		if publicDownloadErr != nil && templateCacheRoot() != "" {
			return useCachedTemplateOnDownloadError(pointer, staging, publicDownloadErr)
		}
	}

	// Fall back to the GitHub CLI (handles private repos and complex URLs).
	commandRunner := exec.NewCommandRunner(&exec.RunnerOptions{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	console := input.NewConsole(
		false, // noPrompt
		true,  // isTerminal
		input.Writers{Output: os.Stdout},
		input.ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
		},
		nil, // formatter
		nil, // externalPromptCfg
	)
	ghCli := github.NewGitHubCli(console, commandRunner)
	if err := ghCli.EnsureInstalled(ctx); err != nil {
		return exterrors.Dependency(
			exterrors.CodeGitHubDownloadFailed,
			fmt.Sprintf("ensuring gh is installed: %s", err),
			"install the GitHub CLI (gh) from https://cli.github.com",
		)
	}

	urlInfo, err := parseGitHubUrlForAdopt(ctx, azdClient, pointer)
	if err != nil {
		return err
	}
	dirPath := parentDirOf(urlInfo.FilePath)
	if err := downloadDirectoryContents(
		ctx, urlInfo.Hostname, urlInfo.RepoSlug, dirPath, dirPath, urlInfo.Branch, staging, ghCli, console,
	); err != nil {
		return exterrors.Dependency(
			exterrors.CodeGitHubDownloadFailed,
			fmt.Sprintf("downloading sample directory: %s", err),
			"verify the URL points to a valid azure.yaml in the repository and you have access",
		)
	}

	hasAzureYaml, err := ensureStagedAzureYaml(staging)
	if err != nil {
		return err
	}
	if !hasAzureYaml {
		return exterrors.Validation(
			exterrors.CodeInvalidManifestPointer,
			"no azure.yaml was found in the downloaded sample directory",
			"verify the URL points to a directory that contains an azure.yaml",
		)
	}
	return nil
}

// parseGitHubUrlForAdopt resolves GitHub repository info for a pointer using the
// azd host (no InitAction required), mirroring (*InitAction).parseGitHubUrl.
func parseGitHubUrlForAdopt(
	ctx context.Context, azdClient *azdext.AzdClient, pointer string,
) (*GitHubUrlInfo, error) {
	urlInfo, err := azdClient.Project().ParseGitHubUrl(ctx, &azdext.ParseGitHubUrlRequest{
		Url: pointer,
	})
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeGitHubDownloadFailed,
			fmt.Sprintf("parsing GitHub URL: %s", err),
			"verify the URL points to a file in a GitHub repository",
		)
	}
	return &GitHubUrlInfo{
		RepoSlug: urlInfo.RepoSlug,
		Branch:   urlInfo.Branch,
		FilePath: urlInfo.FilePath,
		Hostname: urlInfo.Hostname,
	}, nil
}

// parentDirOf returns the directory portion of a repo-relative file path, or ""
// when the file lives at the repository root (so the download lists the root).
func parentDirOf(filePath string) string {
	parts := strings.Split(filePath, "/")
	if len(parts) <= 1 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], "/")
}

// stagedAzureYamlExists reports whether the staging directory contains an
// adopted azure.yaml (or azure.yml) at its root.
func stagedAzureYamlExists(staging string) bool {
	return fileExists(filepath.Join(staging, "azure.yaml")) ||
		fileExists(filepath.Join(staging, "azure.yml"))
}

// ensureFoundryProviderDeclared stamps `infra.provider: microsoft.foundry` onto
// the adopted azure.yaml when the sample didn't already declare it, keeping
// provisioning bicep-less by default.
func ensureFoundryProviderDeclared(ctx context.Context, azdClient *azdext.AzdClient) error {
	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			fmt.Sprintf("failed to get project after adoption: %s", err),
			"",
		)
	}
	if hasFoundryProviderDeclared(resp.Project) {
		return nil
	}
	return writeFoundryProvider(ctx, azdClient)
}

// printAdoptionNextSteps emits context-aware next-step guidance after adoption,
// reusing the shared nextstep resolver. State-assembly errors are intentionally
// ignored: the resolver degrades gracefully on partial state.
func printAdoptionNextSteps(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	folderDisplay string,
	promptOnly bool,
) {
	if promptOnly {
		printPromptInitNextSteps(folderDisplay)
		return
	}
	var stateOpts []nextstep.Option
	if folderDisplay != "" {
		stateOpts = append(stateOpts, nextstep.WithCreatedFolder(folderDisplay))
	}
	state, _ := nextstep.AssembleState(ctx, azdClient, stateOpts...)
	_ = printAllNextIfTerminal(os.Stdout, nextstep.ResolveAfterInit(state, readmeExistsForProject(ctx, azdClient)))
}

// applyDeployModeToAdoptedProject locates the azure.ai.agent service in the
// adopted project and applies deploy-mode configuration (code or container)
// based on the --deploy-mode, --runtime, and --entry-point flags. When no
// explicit flag is passed and the service already has a codeConfiguration or
// docker property, the service is left unchanged (the sample is pre-configured).
//
// It reports whether any agent service requires an Azure Container Registry
// for a source-container build.
func applyDeployModeToAdoptedProject(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
) (bool, error) {
	projectNeedsACR, _, err := applyDeployModeToAdoptedProjectWithSources(ctx, flags, azdClient)
	return projectNeedsACR, err
}

func applyDeployModeToAdoptedProjectWithSources(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
) (bool, []string, error) {
	// Validate --image flag early (incompatible with --deploy-mode code).
	if err := validateImageFlag(flags.image, flags.deployMode); err != nil {
		return false, nil, err
	}

	resp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return false, nil, fmt.Errorf("reading adopted project: %w", err)
	}

	// Collect all agent services in the adopted project.
	type agentEntry struct {
		name string
		svc  *azdext.ServiceConfig
	}
	var agentServices []agentEntry
	for name, svc := range resp.GetProject().GetServices() {
		if svc.GetHost() == AiAgentHost {
			agentServices = append(agentServices, agentEntry{name: name, svc: svc})
		}
	}
	if len(agentServices) == 0 {
		// No agent service found -- nothing to configure.
		return false, nil, nil
	}

	// Apply configuration to each agent service, tracking whether the project
	// contains any source container that requires an ACR. Record only source
	// containers configured by this init so their remote-build decision can be
	// finalized after Foundry project network discovery.
	projectNeedsACR := false
	var configuredSourceContainers []string
	for _, agent := range agentServices {
		kind, err := adoptedAgentKind(agent.svc, resp.GetProject().GetPath())
		if err != nil {
			return false, nil, err
		}
		if kind != "" && kind != "hosted" {
			continue
		}
		hadDockerConfig := adoptedServiceHasDocker(agent.svc)
		serviceNeedsACR, err := applyDeployModeToService(
			ctx,
			flags,
			azdClient,
			resp.GetProject().GetPath(),
			agent.name,
			agent.svc,
		)
		if err != nil {
			return false, nil, err
		}
		projectNeedsACR = projectNeedsACR || serviceNeedsACR
		if serviceNeedsACR && (flags.deployMode != "" || !hadDockerConfig) {
			configuredSourceContainers = append(configuredSourceContainers, agent.name)
		}
	}
	return projectNeedsACR, configuredSourceContainers, nil
}

func adoptedAgentKind(svc *azdext.ServiceConfig, projectRoot string) (string, error) {
	kind, err := agentkind.Kind(svc, projectRoot, "")
	if err != nil {
		return "", fmt.Errorf("resolving adopted agent kind for service %q: %w", svc.GetName(), err)
	}
	return kind, nil
}

func finalizeAdoptedSourceContainerNetwork(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceNames []string,
	networkInjected bool,
) error {
	if len(serviceNames) == 0 {
		return nil
	}
	dockerMap, err := dockerProjectMapForHostedContainer("", networkInjected)
	if err != nil {
		return err
	}
	for _, serviceName := range serviceNames {
		dockerValue, err := structpb.NewValue(dockerMap)
		if err != nil {
			return fmt.Errorf("encoding finalized docker configuration: %w", err)
		}
		if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
			ServiceName: serviceName,
			Path:        "docker",
			Value:       dockerValue,
		}); err != nil {
			return fmt.Errorf("finalizing docker property on agent service %q: %w", serviceName, err)
		}
	}
	return nil
}

func adoptedExternalRegistryConnections(
	projectConfig *azdext.ProjectConfig,
	flagConnection string,
) ([]string, error) {
	if projectConfig == nil {
		return nil, nil
	}

	siblingConnections := map[string]struct{}{}
	for serviceName, service := range projectConfig.GetServices() {
		if service.GetHost() != AiConnectionHost {
			continue
		}
		siblingConnections[serviceName] = struct{}{}
		props, err := resolvedResourceServiceProps(service, projectConfig.GetPath())
		if err != nil {
			return nil, fmt.Errorf("resolving connection service %q: %w", serviceName, err)
		}
		if props == nil {
			continue
		}
		var connection *project.Connection
		if err := project.UnmarshalStruct(props, &connection); err != nil {
			return nil, fmt.Errorf("parsing connection service %q: %w", serviceName, err)
		}
		if connection != nil && strings.TrimSpace(connection.Name) != "" {
			siblingConnections[strings.TrimSpace(connection.Name)] = struct{}{}
		}
	}

	externalConnections := map[string]struct{}{}
	flagConnection = strings.TrimSpace(flagConnection)
	for serviceName, service := range projectConfig.GetServices() {
		if service.GetHost() != AiAgentHost {
			continue
		}
		connectionRef := flagConnection
		if connectionRef == "" {
			resolvedAgent, _, hasDefinition, _, err := project.AgentDefinitionFromResolvedService(
				service, projectConfig.GetPath(),
			)
			if err != nil {
				return nil, fmt.Errorf("reading adopted agent service %q: %w", serviceName, err)
			}
			if hasDefinition {
				connectionRef = strings.TrimSpace(resolvedAgent.RegistryConnectionID)
			}
		}
		if connectionRef == "" {
			continue
		}
		if _, ok := siblingConnections[connectionRef]; !ok {
			externalConnections[connectionRef] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(externalConnections)), nil
}

// applyDeployModeToService applies deploy-mode configuration to a
// single agent service and reports whether the resolved mode requires an ACR.
// Source-container builds require one; code deploy and pre-built images do not.
func applyDeployModeToService(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	projectPath string,
	serviceName string,
	svc *azdext.ServiceConfig,
) (bool, error) {
	resolvedAgent, isHosted, hasDefinition, _, err := project.AgentDefinitionFromResolvedService(svc, projectPath)
	if err != nil {
		return false, fmt.Errorf("reading adopted agent service %q: %w", serviceName, err)
	}
	hasCodeConfig := adoptedServiceHasCodeConfig(svc) ||
		(hasDefinition && resolvedAgent.CodeConfiguration != nil)

	effectiveImage := strings.TrimSpace(flags.image)
	if effectiveImage == "" {
		effectiveImage = strings.TrimSpace(svc.GetImage())
	}
	if effectiveImage == "" && hasDefinition {
		effectiveImage = strings.TrimSpace(resolvedAgent.Image)
	}

	connectionRef := strings.TrimSpace(flags.registryConnection)
	if flags.registryConnection != "" {
		if connectionRef == "" {
			return false, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"registry connection cannot be empty or whitespace",
				"provide the name or ID of an existing Foundry project connection",
			)
		}
		if hasDefinition && !isHosted {
			return false, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"a registry connection is only valid for hosted container agents",
				"use a registry connection with a hosted agent that supplies a pre-built image",
			)
		}
		if flags.deployMode == "code" ||
			(flags.deployMode == "" && flags.image == "" && hasCodeConfig) {
			return false, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"a registry connection cannot be used with code deploy",
				"use the registry connection with a pre-built image or remove it",
			)
		}

		if effectiveImage == "" {
			return false, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"a registry connection requires a pre-built image",
				"pass --image <registry/image:tag> or provide an image in the hosted-agent manifest",
			)
		}
		if err := validateHostedContainerImage(effectiveImage); err != nil {
			return false, err
		}
	}

	writeRegistryConnection := func() error {
		if connectionRef == "" {
			return nil
		}
		connectionValue, err := structpb.NewValue(connectionRef)
		if err != nil {
			return fmt.Errorf("encoding registry connection value: %w", err)
		}
		if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
			ServiceName: serviceName,
			Path:        "registryConnectionId",
			Value:       connectionValue,
		}); err != nil {
			return fmt.Errorf("writing registry connection to agent service %q: %w", serviceName, err)
		}
		return nil
	}

	// Apply --image override to the agent service when provided.
	if flags.image != "" {
		imageValue, err := structpb.NewValue(flags.image)
		if err != nil {
			return false, fmt.Errorf("encoding image value: %w", err)
		}
		if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
			ServiceName: serviceName,
			Path:        "image",
			Value:       imageValue,
		}); err != nil {
			return false, fmt.Errorf("writing image to agent service %q: %w", serviceName, err)
		}
		log.Printf("Applied --image %q to agent service %q", flags.image, serviceName)

		// --image implies container deploy; apply image passthrough and return.
		if err := applyContainerDeployToService(ctx, azdClient, serviceName, svc, flags.image); err != nil {
			return false, err
		}
		if err := writeRegistryConnection(); err != nil {
			return false, err
		}
		return false, nil
	}

	// Check whether the service already specifies its deploy mode. Code deploy
	// takes precedence over stale image or docker properties when no override
	// is requested.
	hasDocker := adoptedServiceHasDocker(svc)
	if flags.deployMode == "" && hasCodeConfig {
		return false, nil
	}

	// An adopted service that already declares an image also uses passthrough,
	// even when --image was not supplied during this init. Legacy definitions
	// may carry the image in extension properties, so promote the resolved value
	// to the core service image field. An explicit code mode overrides a leftover image.
	if effectiveImage != "" && flags.deployMode != "code" {
		if err := validateHostedContainerImage(effectiveImage); err != nil {
			return false, err
		}
		if strings.TrimSpace(svc.GetImage()) == "" {
			imageValue, err := structpb.NewValue(effectiveImage)
			if err != nil {
				return false, fmt.Errorf("encoding resolved image value: %w", err)
			}
			if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
				ServiceName: serviceName,
				Path:        "image",
				Value:       imageValue,
			}); err != nil {
				return false, fmt.Errorf("promoting resolved image on agent service %q: %w", serviceName, err)
			}
		}
		if err := applyContainerDeployToService(ctx, azdClient, serviceName, svc, effectiveImage); err != nil {
			return false, err
		}
		if err := writeRegistryConnection(); err != nil {
			return false, err
		}
		return false, nil
	}

	// When no explicit --deploy-mode flag is passed and the service is already
	// configured for a source-container build, respect that configuration.
	if flags.deployMode == "" && hasDocker {
		return true, nil
	}

	// Use the service's subdirectory for language detection (not project root).
	targetDir := svc.GetRelativePath()
	if targetDir == "" {
		targetDir = "."
	}
	serviceDir, err := paths.JoinAllowRoot(projectPath, targetDir)
	if err != nil {
		return false, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("invalid service path for %s: %s", serviceName, err),
			"update azure.yaml so the agent service path stays within the project directory",
		)
	}
	showCodeDeploy := supportsCodeDeploy(serviceDir)
	// userProvidedManifest is true: -m was explicitly provided.
	deployMode, err := promptDeployMode(
		ctx, azdClient, flags.noPrompt, showCodeDeploy, flags.deployMode, true,
	)
	if err != nil {
		return false, fmt.Errorf("resolving deploy mode for adopted project: %w", err)
	}

	if deployMode == "code" {
		return false, applyCodeDeployToService(
			ctx, flags, azdClient, serviceName, serviceDir, svc,
		)
	}
	if err := applyContainerDeployToService(ctx, azdClient, serviceName, svc, ""); err != nil {
		return false, err
	}
	return true, nil
}

// adoptedServiceHasCodeConfig checks whether the adopted agent service already
// declares a codeConfiguration in its properties.
func adoptedServiceHasCodeConfig(svc *azdext.ServiceConfig) bool {
	props := svc.GetAdditionalProperties()
	if props == nil {
		return false
	}
	fields := props.GetFields()
	if fields == nil {
		return false
	}
	v, ok := fields["codeConfiguration"]
	if !ok {
		return false
	}
	// A null value doesn't count as having a codeConfiguration.
	return v != nil && v.GetStructValue() != nil
}

// adoptedServiceHasDocker checks whether the adopted agent service already
// declares a docker configuration in its properties. We check
// additionalProperties rather than svc.GetDocker() because the gRPC mapper
// always returns a non-nil Docker pointer (even for the zero-value struct).
func adoptedServiceHasDocker(svc *azdext.ServiceConfig) bool {
	props := svc.GetAdditionalProperties()
	if props == nil {
		return false
	}
	fields := props.GetFields()
	if fields == nil {
		return false
	}
	v, ok := fields["docker"]
	if !ok {
		return false
	}
	// A null value doesn't count as having docker configured.
	return v != nil && v.GetStructValue() != nil
}

// applyCodeDeployToService writes codeConfiguration onto the adopted agent
// service and updates the service language from "docker" to the appropriate
// language for the selected runtime.
func applyCodeDeployToService(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	serviceName string,
	targetDir string,
	svc *azdext.ServiceConfig,
) error {
	codeConfig, err := promptCodeConfig(ctx, azdClient, targetDir, flags.noPrompt, codeDeployOptions{
		runtime:       flags.runtime,
		entryPoint:    flags.entryPoint,
		depResolution: flags.depResolution,
	}, true) // userProvidedManifest=true since -m was provided
	if err != nil {
		return fmt.Errorf("resolving code configuration for adopted project: %w", err)
	}

	// Write codeConfiguration onto the service (camelCase keys match the
	// azure.yaml inline format read by the deploy path via JSON unmarshal).
	codeConfigMap := map[string]any{
		"runtime":    codeConfig.Runtime,
		"entryPoint": codeConfig.EntryPoint,
	}
	if codeConfig.DependencyResolution != nil {
		codeConfigMap["dependencyResolution"] = *codeConfig.DependencyResolution
	}

	codeConfigValue, err := structpb.NewValue(codeConfigMap)
	if err != nil {
		return fmt.Errorf("encoding codeConfiguration: %w", err)
	}

	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "codeConfiguration",
		Value:       codeConfigValue,
	}); err != nil {
		return fmt.Errorf("writing codeConfiguration to agent service: %w", err)
	}

	// Update the service language to match the runtime.
	language := "python"
	if strings.HasPrefix(codeConfig.Runtime, "dotnet_") {
		language = "csharp"
	}
	langValue, err := structpb.NewValue(language)
	if err != nil {
		return fmt.Errorf("encoding language value: %w", err)
	}
	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "language",
		Value:       langValue,
	}); err != nil {
		return fmt.Errorf("updating service language to %s: %w", language, err)
	}

	// Remove docker property if it was previously set (switching from container to code).
	if adoptedServiceHasDocker(svc) {
		if _, err := azdClient.Project().UnsetServiceConfig(ctx, &azdext.UnsetServiceConfigRequest{
			ServiceName: serviceName,
			Path:        "docker",
		}); err != nil {
			log.Printf("warning: could not clear docker property on service %q: %v", serviceName, err)
		}
	}

	log.Printf("Applied code deploy configuration (runtime=%s, entryPoint=%s) to service %q",
		codeConfig.Runtime, codeConfig.EntryPoint, serviceName)
	return nil
}

// applyContainerDeployToService sets the docker property on the adopted agent
// service and ensures the language is "docker". Removes any codeConfiguration
// if present.
func applyContainerDeployToService(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	serviceName string,
	svc *azdext.ServiceConfig,
	image string,
) error {
	dockerMap, err := dockerProjectMapForHostedContainer(image, false)
	if err != nil {
		return err
	}
	dockerValue, err := structpb.NewValue(dockerMap)
	if err != nil {
		return fmt.Errorf("encoding docker configuration: %w", err)
	}

	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "docker",
		Value:       dockerValue,
	}); err != nil {
		return fmt.Errorf("writing docker property to agent service: %w", err)
	}

	// Set language to docker.
	langValue, err := structpb.NewValue("docker")
	if err != nil {
		return fmt.Errorf("encoding language value: %w", err)
	}
	if _, err := azdClient.Project().SetServiceConfigValue(ctx, &azdext.SetServiceConfigValueRequest{
		ServiceName: serviceName,
		Path:        "language",
		Value:       langValue,
	}); err != nil {
		return fmt.Errorf("updating service language to docker: %w", err)
	}

	// Remove codeConfiguration if present (switching from code to container).
	if adoptedServiceHasCodeConfig(svc) {
		if _, err := azdClient.Project().UnsetServiceConfig(ctx, &azdext.UnsetServiceConfigRequest{
			ServiceName: serviceName,
			Path:        "codeConfiguration",
		}); err != nil {
			log.Printf("warning: could not clear codeConfiguration on service %q: %v", serviceName, err)
		}
	}

	log.Printf("Applied container deploy configuration to service %q", serviceName)
	return nil
}
