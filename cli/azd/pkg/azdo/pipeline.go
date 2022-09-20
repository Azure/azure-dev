// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/microsoft/azure-devops-go-api/azuredevops"
	"github.com/microsoft/azure-devops-go-api/azuredevops/build"
	"github.com/microsoft/azure-devops-go-api/azuredevops/taskagent"
)

// Creates a variable to be associated with a Pipeline
func createBuildDefinitionVariable(value string, isSecret bool, allowOverride bool) build.BuildDefinitionVariable {
	return build.BuildDefinitionVariable{
		AllowOverride: &allowOverride,
		IsSecret:      &isSecret,
		Value:         &value,
	}
}

// returns the default agent queue. This is used to associate a Pipeline with a default agent pool queue
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

// find pipeline by name
func pipelineExists(
	ctx context.Context,
	client *build.Client,
	projectId *string,
	pipelineName *string,
) (bool, error) {
	getDefinitionsArgs := build.GetDefinitionsArgs{
		Project: projectId,
		Name:    pipelineName,
	}

	buildDefinitionsResponse, err := (*client).GetDefinitions(ctx, getDefinitionsArgs)
	if err != nil {
		return false, err
	}
	buildDefinitions := buildDefinitionsResponse.Value
	for _, definition := range buildDefinitions {
		if *definition.Name == *pipelineName {
			return true, nil
		}
	}
	return false, nil
}

// create a new Azure DevOps pipeline
func CreatePipeline(
	ctx context.Context,
	projectId string,
	name string,
	repoName string,
	connection *azuredevops.Connection,
	credentials AzureServicePrincipalCredentials,
	env environment.Environment,
	console input.Console,
	provisioningProvider provisioning.Options) (*build.BuildDefinition, error) {

	client, err := build.NewClient(ctx, connection)
	if err != nil {
		return nil, err
	}

	var exists bool = true
	var count = 0
	var maxTries = 4
	for exists {
		exists, err = pipelineExists(ctx, &client, &projectId, &name)
		if err != nil {
			return nil, err
		}
		count = count + 1

		if exists {
			name = fmt.Sprintf("%s - %s (%d)", name, repoName, count)
		} else {
			continue
		}

		if count >= maxTries {
			return nil, fmt.Errorf("error creating new pipeline")
		}
	}

	queue, err := getAgentQueue(ctx, projectId, connection)
	if err != nil {
		return nil, err
	}

	createDefinitionArgs, err := createAzureDevPipelineArgs(
		ctx, projectId, name, repoName, credentials, env, queue, provisioningProvider)
	if err != nil {
		return nil, err
	}

	newBuildDefinition, err := client.CreateDefinition(ctx, *createDefinitionArgs)
	if err != nil {
		return nil, err
	}

	return newBuildDefinition, nil
}

// create Azure Deploy Pipeline parameters
func createAzureDevPipelineArgs(
	ctx context.Context,
	projectId string,
	name string,
	repoName string,
	credentials AzureServicePrincipalCredentials,
	env environment.Environment,
	queue *taskagent.TaskAgentQueue,
	provisioningProvider provisioning.Options,
) (*build.CreateDefinitionArgs, error) {

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
	if provisioningProvider.Provider == provisioning.Terraform {
		variables["ARM_TENANT_ID"] = createBuildDefinitionVariable(credentials.TenantId, false, false)
		variables["ARM_CLIENT_ID"] = createBuildDefinitionVariable(credentials.ClientId, true, false)
		variables["ARM_CLIENT_SECRET"] = createBuildDefinitionVariable(credentials.ClientSecret, true, false)
	}
	variables["AZURE_LOCATION"] = createBuildDefinitionVariable(env.GetLocation(), false, false)
	variables["AZURE_ENV_NAME"] = createBuildDefinitionVariable(env.GetEnvName(), false, false)
	variables["AZURE_SERVICE_CONNECTION"] = createBuildDefinitionVariable(ServiceConnectionName, false, false)

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
	return createDefinitionArgs, nil
}

// run a pipeline. This is used to invoke the deploy pipeline after a successful push of the code
func QueueBuild(
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
