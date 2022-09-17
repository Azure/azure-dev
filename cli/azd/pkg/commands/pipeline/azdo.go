package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"

	"github.com/microsoft/azure-devops-go-api/azuredevops"
	"github.com/microsoft/azure-devops-go-api/azuredevops/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/core"
	"github.com/microsoft/azure-devops-go-api/azuredevops/git"
	"github.com/microsoft/azure-devops-go-api/azuredevops/operations"
	"github.com/microsoft/azure-devops-go-api/azuredevops/policy"
	"github.com/microsoft/azure-devops-go-api/azuredevops/serviceendpoint"
	"github.com/microsoft/azure-devops-go-api/azuredevops/taskagent"
)

var (
	AzDoHostName                 = "dev.azure.com"         // hostname of the AzDo PaaS service.
	AzDoPatName                  = "AZURE_DEVOPS_EXT_PAT"  // environment variable that holds the Azure DevOps PAT
	AzDoEnvironmentOrgName       = "AZURE_DEVOPS_ORG_NAME" //  environment variable that holds the Azure DevOps Organization Name
	AzDoEnvironmentProjectIdName = "AZURE_DEVOPS_PROJECT_ID"
	AzDoEnvironmentProjectName   = "AZURE_DEVOPS_PROJECT_NAME"
	AzDoEnvironmentRepoIdName    = "AZURE_DEVOPS_REPOSITORY_ID"
	AzDoEnvironmentRepoName      = "AZURE_DEVOPS_REPOSITORY_NAME"
	AzDoEnvironmentRepoWebUrl    = "AZURE_DEVOPS_REPOSITORY_WEB_URL"
	AzdoConfigSuccessMessage     = "\nSuccessfully configured Azure DevOps Repository %s\n"
	AzurePipelineName            = "Azure Dev Deploy"
	AzurePipelineYamlPath        = ".azdo/pipelines/azure-dev.yml"
	CloudEnvironment             = "AzureCloud"
	DefaultBranch                = "master"
)

type AzDoClient struct {
	core.Client
}

func ensureAzdoConfigExists(ctx context.Context, env *environment.Environment, key string, label string) (string, error) {
	value := env.Values[key]
	if value != "" {
		return value, nil
	}

	value, exists := os.LookupEnv(key)
	if !exists || value == "" {
		return value, fmt.Errorf("%s not found in environment variable %s", label, key)
	}
	return value, nil
}

func ensureAzdoPatExists(ctx context.Context, env *environment.Environment) (string, error) {
	return ensureAzdoConfigExists(ctx, env, AzDoPatName, "azure devops personal access token")
}

func ensureAzdoOrgNameExists(ctx context.Context, env *environment.Environment) (string, error) {
	return ensureAzdoConfigExists(ctx, env, AzDoEnvironmentOrgName, "azure devops organization name")
}

func getAzdoConnection(ctx context.Context, organization string, personalAccessToken string) *azuredevops.Connection {
	organizationUrl := fmt.Sprintf("https://dev.azure.com/%s", organization)
	connection := azuredevops.NewPatConnection(organizationUrl, personalAccessToken)
	return connection
}

func getAzDoDefaultGitRepositoriesInProject(ctx context.Context, projectName string, connection *azuredevops.Connection) (*git.GitRepository, error) {
	gitClient, err := git.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}

	includeLinks := true
	includeAllUrls := true
	repoArgs := git.GetRepositoriesArgs{
		Project:        &projectName,
		IncludeLinks:   &includeLinks,
		IncludeAllUrls: &includeAllUrls,
	}

	getRepositoriesResult, err := gitClient.GetRepositories(ctx, repoArgs)
	if err != nil {
		return nil, err
	}
	repos := *getRepositoriesResult

	for _, repo := range repos {
		if *repo.Name == projectName {
			return &repo, nil
		}
	}

	return nil, fmt.Errorf("error finding default git repository %s in project %s", projectName)
}

func getAzDoGitRepositoriesInProject(ctx context.Context, projectName string, orgName string, connection *azuredevops.Connection, console input.Console) (*git.GitRepository, error) {
	gitClient, err := git.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}

	includeLinks := true
	includeAllUrls := true
	repoArgs := git.GetRepositoriesArgs{
		Project:        &projectName,
		IncludeLinks:   &includeLinks,
		IncludeAllUrls: &includeAllUrls,
	}

	getRepositoriesResult, err := gitClient.GetRepositories(ctx, repoArgs)
	if err != nil {
		return nil, err
	}
	repos := *getRepositoriesResult

	options := make([]string, len(repos))
	for idx, repo := range repos {
		options[idx] = *repo.Name
	}
	repoIdx, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Please choose an existing Azure DevOps Repository",
		Options: options,
	})

	if err != nil {
		return nil, fmt.Errorf("prompting for azdo project: %w", err)
	}
	selectedRepoName := options[repoIdx]
	for _, repo := range repos {
		if selectedRepoName == *repo.Name {
			return &repo, nil
		}
	}

	return nil, fmt.Errorf("error finding git repository %s in organization %s", selectedRepoName, orgName)
}

func createRepository(ctx context.Context, projectId string, repoName string, connection *azuredevops.Connection) (*git.GitRepository, error) {
	gitClient, err := git.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}

	gitRepositoryCreateOptions := git.GitRepositoryCreateOptions{
		Name: &repoName,
	}

	createRepositoryArgs := git.CreateRepositoryArgs{
		Project:               &projectId,
		GitRepositoryToCreate: &gitRepositoryCreateOptions,
	}
	repo, err := gitClient.CreateRepository(ctx, createRepositoryArgs)
	if err != nil {
		return nil, err
	}
	return repo, nil
}

func getProcessTemplateId(ctx context.Context, client core.Client) (string, error) {
	processArgs := core.GetProcessesArgs{}
	processes, err := client.GetProcesses(ctx, processArgs)
	if err != nil {
		return "", err
	}
	process := (*processes)[0]
	return fmt.Sprintf("%s", process.Id), nil
}

func createProject(ctx context.Context, connection *azuredevops.Connection, name string, description string, console input.Console) (*core.TeamProjectReference, error) {
	coreClient, err := core.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}

	processTemplateId, err := getProcessTemplateId(ctx, coreClient)
	if err != nil {
		return nil, fmt.Errorf("error fetching process template id %w", err)
	}

	capabilities := map[string]map[string]string{
		"versioncontrol": {
			"sourceControlType": "git",
		},
		"processTemplate": {
			"templateTypeId": processTemplateId,
		},
	}
	args := core.QueueCreateProjectArgs{
		ProjectToCreate: &core.TeamProject{
			Description:  &description,
			Name:         &name,
			Visibility:   &core.ProjectVisibilityValues.Private,
			Capabilities: &capabilities,
		},
	}
	res, err := coreClient.QueueCreateProject(ctx, args)
	if err != nil {
		return nil, err
	}

	operationsClient := operations.NewClient(ctx, connection)

	getOperationsArgs := operations.GetOperationArgs{
		OperationId: res.Id,
	}

	projectCreated := false
	maxCheck := 10
	count := 0

	for !projectCreated {
		operation, err := operationsClient.GetOperation(ctx, getOperationsArgs)
		if err != nil {
			return nil, err
		}

		if *operation.Status == "succeeded" {
			projectCreated = true
		}

		if count >= maxCheck {
			return nil, fmt.Errorf("error creating azure devops project %s", name)
		}

		count++
		time.Sleep(700 * time.Millisecond)
	}

	project, err := getAzdoProjectByName(ctx, connection, name)
	if err != nil {
		return nil, err
	}
	return project, nil
}

func getAzdoProjectFromNew(ctx context.Context, repoPath string, connection *azuredevops.Connection, env *environment.Environment, console input.Console) (string, string, error) {
	var project *core.TeamProjectReference
	currentFolderName := filepath.Base(repoPath)
	var projectDescription string = "Azure Dev CLI Project"

	for {
		name, err := console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Enter the name for your new Azure Devops Project OR Hit enter to use this name:",
			DefaultValue: currentFolderName,
		})
		if err != nil {
			return "", "", fmt.Errorf("asking for new project name: %w", err)
		}
		var message string = ""
		newProject, err := createProject(ctx, connection, name, projectDescription, console)
		if err != nil {
			message = err.Error()
		}
		if strings.Contains(message, fmt.Sprintf("The following project already exists on the Azure DevOps Server: %s", name)) {
			console.Message(ctx, fmt.Sprintf("error: the project name '%s' is already in use\n", name))
			continue // try again
		} else if strings.Contains(message, "The following name is not valid") {
			console.Message(ctx, fmt.Sprintf("error: the project name '%s' is not a valid Azure DevOps project Name. See https://docs.microsoft.com/en-us/azure/devops/organizations/settings/naming-restrictions?view=azure-devops#project-names\n", name))
			continue // try again
		} else if err != nil {
			return "", "", fmt.Errorf("creating project: %w", err)
		} else {
			project = newProject
			break
		}
	}

	return *project.Name, project.Id.String(), nil
}

func getAzdoProjectByName(ctx context.Context, connection *azuredevops.Connection, name string) (*core.TeamProjectReference, error) {
	coreClient, err := core.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}

	args := core.GetProjectsArgs{}
	getProjectsResponse, err := coreClient.GetProjects(ctx, args)
	if err != nil {
		return nil, err
	}

	projects := getProjectsResponse.Value
	for _, project := range projects {
		if *project.Name == name {
			return &project, nil
		}
	}

	return nil, fmt.Errorf("azure devops project %s not found", name)
}

func getAzdoProjectFromExisting(ctx context.Context, connection *azuredevops.Connection, console input.Console) (string, string, error) {
	coreClient, err := core.NewClient(ctx, connection)
	if err != nil {
		return "", "", err
	}

	args := core.GetProjectsArgs{}
	getProjectsResponse, err := coreClient.GetProjects(ctx, args)
	if err != nil {
		return "", "", err
	}

	projects := getProjectsResponse.Value
	projectsList := make([]core.TeamProjectReference, len(projects))
	options := make([]string, len(projects))
	for idx, project := range projects {
		options[idx] = *project.Name
		projectsList[idx] = project
	}

	projectIdx, err := console.Select(ctx, input.ConsoleOptions{
		Message: "Please choose an existing Azure DevOps Project",
		Options: options,
	})

	if err != nil {
		return "", "", fmt.Errorf("prompting for azdo project: %w", err)
	}

	return options[projectIdx], projectsList[projectIdx].Id.String(), nil
}

func createBuildDefinitionVariable(value string, isSecret bool, allowOverride bool) build.BuildDefinitionVariable {
	return build.BuildDefinitionVariable{
		AllowOverride: &allowOverride,
		IsSecret:      &isSecret,
		Value:         &value,
	}
}

func getAgentQueue(ctx context.Context, projectId string, connection *azuredevops.Connection) (*taskagent.TaskAgentQueue, error) {
	client, err := taskagent.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}
	getAgentQueuesArgs := taskagent.GetAgentQueuesArgs{
		Project: &projectId,
	}
	queues, err := client.GetAgentQueues(ctx, getAgentQueuesArgs)
	if err != nil {
		return nil, err
	}
	for _, queue := range *queues {
		if *queue.Name == "Default" {
			return &queue, nil
		}
	}
	return nil, fmt.Errorf("could not find a default agent queue in project %s", projectId)
}

func createPipeline(
	ctx context.Context,
	projectId string,
	name string,
	repoName string,
	connection *azuredevops.Connection,
	credentials AzureServicePrincipalCredentials,
	env environment.Environment) (*build.BuildDefinition, error) {

	client, err := build.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}

	repoType := "tfsgit"
	buildDefinitionType := build.DefinitionType("build")
	definitionQueueStatus := build.DefinitionQueueStatus("enabled")
	defaultBranch := fmt.Sprintf("refs/heads/%s", DefaultBranch)
	buildRepository := &build.BuildRepository{
		Type:          &repoType,
		Name:          &repoName,
		DefaultBranch: &defaultBranch,
	}

	process := make(map[string]interface{})
	process["type"] = 2
	process["yamlFilename"] = AzurePipelineYamlPath

	variables := make(map[string]build.BuildDefinitionVariable)
	variables["AZURE_SUBSCRIPTION_ID"] = createBuildDefinitionVariable(credentials.SubscriptionId, false, false)
	variables["ARM_TENANT_ID"] = createBuildDefinitionVariable(credentials.TenantId, false, false)
	variables["ARM_CLIENT_ID"] = createBuildDefinitionVariable(credentials.ClientId, true, false)
	variables["ARM_CLIENT_SECRET"] = createBuildDefinitionVariable(credentials.ClientSecret, true, false)
	variables["AZURE_LOCATION"] = createBuildDefinitionVariable(env.GetLocation(), false, false)
	variables["AZURE_ENV_NAME"] = createBuildDefinitionVariable(env.GetEnvName(), false, false)

	queue, err := getAgentQueue(ctx, projectId, connection)
	if err != nil {
		return nil, err
	}

	agentPoolQueue := &build.AgentPoolQueue{
		Id:   queue.Id,
		Name: queue.Name,
	}

	trigger := make(map[string]interface{})
	trigger["batchChanges"] = false
	trigger["maxConcurrentBuildsPerBranch"] = 1
	trigger["pollingInterval"] = 0
	trigger["isSettingsSourceOptionSupported"] = true
	trigger["defaultSettingsSourceType"] = 2
	trigger["settingsSourceType"] = 2
	trigger["defaultSettingsSourceType"] = 2
	trigger["triggerType"] = 2

	triggers := make([]interface{}, 1)
	triggers[0] = trigger

	buildDefinition := &build.BuildDefinition{
		Name:        &name,
		Type:        &buildDefinitionType,
		QueueStatus: &definitionQueueStatus,
		Repository:  buildRepository,
		Process:     process,
		Queue:       agentPoolQueue,
		Variables:   &variables,
		Triggers:    &triggers,
	}

	createDefinitionArgs := &build.CreateDefinitionArgs{
		Project:    &projectId,
		Definition: buildDefinition,
	}

	newBuildDefinition, err := client.CreateDefinition(ctx, *createDefinitionArgs)
	if err != nil {
		return nil, err
	}

	return newBuildDefinition, nil
}

func queueBuild(
	ctx context.Context,
	connection *azuredevops.Connection,
	projectId string,
	buildDefinition *build.BuildDefinition) error {
	client, err := build.NewClient(ctx, connection)
	if err != nil {
		return err
	}
	definitionReference := &build.DefinitionReference{
		Id: buildDefinition.Id,
	}

	newBuild := &build.Build{
		Definition: definitionReference,
	}
	queueBuildArgs := build.QueueBuildArgs{
		Project: &projectId,
		Build:   newBuild,
	}

	//time.Sleep(500 * time.Millisecond)

	_, err = client.QueueBuild(ctx, queueBuildArgs)
	if err != nil {
		return err
	}

	return nil
}
func authorizeServiceConnectionToAllPipelines(
	ctx context.Context,
	projectId string,
	endpoint *serviceendpoint.ServiceEndpoint,
	connection *azuredevops.Connection) error {
	buildClient, err := build.NewClient(ctx, connection)
	if err != nil {
		return err
	}

	endpointResource := "endpoint"
	endpointAuthorized := true
	endpointId := endpoint.Id.String()
	resources := make([]build.DefinitionResourceReference, 1)
	resources[0] = build.DefinitionResourceReference{
		Type:       &endpointResource,
		Authorized: &endpointAuthorized,
		Id:         &endpointId,
	}

	authorizeProjectResourcesArgs := build.AuthorizeProjectResourcesArgs{
		Project:   &projectId,
		Resources: &resources,
	}

	_, err = buildClient.AuthorizeProjectResources(ctx, authorizeProjectResourcesArgs)

	if err != nil {
		return err
	}
	return nil
}

func createServiceConnection(
	ctx context.Context,
	connection *azuredevops.Connection,
	projectId string,
	azdEnvironment environment.Environment,
	repoDetails *gitRepositoryDetails,
	credentials AzureServicePrincipalCredentials,
	console input.Console) error {

	client, err := serviceendpoint.NewClient(ctx, connection)
	if err != nil {
		return err
	}

	endpointType := "azurerm"
	endpointOwner := "library"
	endpointUrl := "https://management.azure.com/"
	endpointName := "azconnection"
	endpointIsShared := false
	endpointScheme := "ServicePrincipal"

	endpointAuthorizationParameters := make(map[string]string)
	endpointAuthorizationParameters["serviceprincipalid"] = credentials.ClientId
	endpointAuthorizationParameters["serviceprincipalkey"] = credentials.ClientSecret
	endpointAuthorizationParameters["authenticationType"] = "spnKey"
	endpointAuthorizationParameters["tenantid"] = credentials.TenantId

	endpointData := make(map[string]string)
	endpointData["environment"] = CloudEnvironment
	endpointData["subscriptionId"] = credentials.SubscriptionId
	endpointData["subscriptionName"] = "azure subscription"
	endpointData["scopeLevel"] = "Subscription"
	endpointData["creationMode"] = "Manual"

	endpointAuthorization := serviceendpoint.EndpointAuthorization{
		Scheme:     &endpointScheme,
		Parameters: &endpointAuthorizationParameters,
	}
	serviceEndpoint := &serviceendpoint.ServiceEndpoint{
		Type:          &endpointType,
		Owner:         &endpointOwner,
		Url:           &endpointUrl,
		Name:          &endpointName,
		IsShared:      &endpointIsShared,
		Authorization: &endpointAuthorization,
		Data:          &endpointData,
	}
	createServiceEndpointArgs := serviceendpoint.CreateServiceEndpointArgs{
		Project:  &projectId,
		Endpoint: serviceEndpoint,
	}

	endpoint, err := client.CreateServiceEndpoint(ctx, createServiceEndpointArgs)
	if err != nil {
		return err
	}

	authorizeServiceConnectionToAllPipelines(ctx, projectId, endpoint, connection)

	return nil
}

func getBuildType(ctx context.Context, projectId *string, policyClient policy.Client) (*policy.PolicyType, error) {
	getPolicyTypesArgs := policy.GetPolicyTypesArgs{
		Project: projectId,
	}
	policyTypes, err := policyClient.GetPolicyTypes(ctx, getPolicyTypesArgs)
	if err != nil {
		return nil, err
	}
	for _, policy := range *policyTypes {
		if *policy.DisplayName == "Build" {
			return &policy, nil
		}
	}
	return nil, fmt.Errorf("could not find 'Build' policy type in project")
}

func createBuildPolicy(
	ctx context.Context,
	connection *azuredevops.Connection,
	projectId string,
	repoId string,
	buildDefinition *build.BuildDefinition) error {
	client, err := policy.NewClient(ctx, connection)
	if err != nil {
		return err
	}

	buildPolicyType, err := getBuildType(ctx, &projectId, client)
	if err != nil {
		return err
	}

	policyTypeRef := &policy.PolicyTypeRef{
		Id: buildPolicyType.Id,
	}
	policyRevision := 1
	policyIsDeleted := false
	policyIsBlocking := true
	policyIsEnabled := true

	policySettingsScope := make(map[string]interface{})
	policySettingsScope["repositoryId"] = repoId
	policySettingsScope["refName"] = fmt.Sprintf("refs/heads/%s", DefaultBranch)
	policySettingsScope["matchKind"] = "Exact"

	policySettingsScopes := make([]map[string]interface{}, 1)
	policySettingsScopes[0] = policySettingsScope

	policySettings := make(map[string]interface{})
	policySettings["buildDefinitionId"] = buildDefinition.Id
	policySettings["displayName"] = "Azure Dev Deploy PR"
	policySettings["manualQueueOnly"] = false
	policySettings["queueOnSourceUpdateOnly"] = true
	policySettings["validDuration"] = 720
	policySettings["scope"] = policySettingsScopes

	policyConfiguration := &policy.PolicyConfiguration{
		Type:       policyTypeRef,
		Revision:   &policyRevision,
		IsDeleted:  &policyIsDeleted,
		IsBlocking: &policyIsBlocking,
		IsEnabled:  &policyIsEnabled,
		Settings:   policySettings,
	}

	createPolicyConfigurationArgs := policy.CreatePolicyConfigurationArgs{
		Project:       &projectId,
		Configuration: policyConfiguration,
	}
	client.CreatePolicyConfiguration(ctx, createPolicyConfigurationArgs)

	return nil
}
