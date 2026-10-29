// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
)

type serviceTarget struct {
	azdext.BaseServiceTargetProvider
	client    *azdext.AzdClient
	runtime   *Runtime
	modules   moduleDeployer
	resources containerAppResourceService
}

// NewServiceTarget creates the Spring Boot service target.
func NewServiceTarget(client *azdext.AzdClient, runtime *Runtime) azdext.ServiceTargetProvider {
	return newServiceTarget(client, runtime, newAzureModuleDeployer(client, io.Discard), newContainerAppResourceService(client))
}

func newServiceTarget(
	client *azdext.AzdClient,
	runtime *Runtime,
	modules moduleDeployer,
	resources containerAppResourceService,
) *serviceTarget {
	return &serviceTarget{
		client:    client,
		runtime:   runtime,
		modules:   modules,
		resources: resources,
	}
}

func (t *serviceTarget) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	return t.runtime.ValidateService(ctx, serviceConfig)
}

func (t *serviceTarget) GetTargetResource(
	ctx context.Context,
	subscriptionID string,
	serviceConfig *azdext.ServiceConfig,
	defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	envName, values, err := t.runtime.environments.values(ctx)
	if err != nil {
		return nil, err
	}
	return t.resources.Resolve(
		ctx,
		subscriptionID,
		envName,
		values,
		serviceConfig,
		defaultResolver,
	)
}

func (t *serviceTarget) Package(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	progress azdext.ProgressReporter,
) (*azdext.ServicePackageResult, error) {
	projectResponse, err := t.client.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, fmt.Errorf("getting the azd project for container packaging: %w", err)
	}
	project := projectResponse.GetProject()
	if project == nil {
		return nil, validationError(
			"missing_project_config",
			"project configuration is unavailable during container packaging",
			"run the command from an initialized azd project",
		)
	}

	staged, err := prepareContainerContext(ctx, project, serviceConfig, serviceContext)
	if err != nil {
		return nil, err
	}

	progress("Building Spring Boot container")
	buildResponse, buildErr := t.client.Container().Build(ctx, &azdext.ContainerBuildRequest{
		ServiceName:    serviceConfig.GetName(),
		ServiceContext: serviceContext,
	})
	cleanupErr := staged.cleanup()
	buildErr = nonDisclosureError(buildErr)
	if buildErr != nil || cleanupErr != nil {
		return nil, errors.Join(buildErr, cleanupErr)
	}
	if buildResponse.GetResult() == nil || len(buildResponse.GetResult().GetArtifacts()) == 0 {
		return nil, validationError(
			"missing_container_build_artifact",
			"the container build returned no artifacts",
			"verify Docker is running and the generated Dockerfile is included in the build context",
		)
	}
	serviceContext.Build = append(serviceContext.Build, buildResponse.GetResult().GetArtifacts()...)

	progress("Packaging Spring Boot container")
	packageResponse, err := t.client.Container().Package(ctx, &azdext.ContainerPackageRequest{
		ServiceName:    serviceConfig.GetName(),
		ServiceContext: serviceContext,
	})
	if err != nil {
		return nil, fmt.Errorf("packaging the Spring Boot container: %w", nonDisclosureError(err))
	}
	if packageResponse.GetResult() == nil {
		return nil, validationError(
			"missing_container_package_result",
			"the container package operation returned no result",
			"verify Docker is running and retry azd package",
		)
	}
	return packageResponse.GetResult(), nil
}

func (t *serviceTarget) Publish(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	publishOptions *azdext.PublishOptions,
	progress azdext.ProgressReporter,
) (*azdext.ServicePublishResult, error) {
	if targetResource == nil ||
		targetResource.GetSubscriptionId() == "" ||
		targetResource.GetResourceGroupName() == "" ||
		targetResource.GetResourceName() == "" {
		return nil, validationError(
			"missing_target_resource",
			fmt.Sprintf("service %q has no resolved Container App target", serviceConfig.GetName()),
			"run azd provision to restore the authored infrastructure outputs",
		)
	}

	progress("Publishing Spring Boot container")
	response, err := t.client.Container().Publish(ctx, &azdext.ContainerPublishRequest{
		ServiceName:    serviceConfig.GetName(),
		ServiceContext: serviceContext,
	})
	if err != nil {
		return nil, fmt.Errorf("publishing the Spring Boot container: %w", nonDisclosureError(err))
	}
	if response.GetResult() == nil {
		return nil, validationError(
			"missing_container_publish_result",
			"the container publish operation returned no result",
			"verify the target registry and retry azd deploy",
		)
	}
	imageName, err := remoteContainerImage(response.GetResult().GetArtifacts())
	if err != nil {
		return nil, err
	}
	envName, _, err := t.runtime.environments.values(ctx)
	if err != nil {
		return nil, err
	}
	if err := t.runtime.environments.set(
		ctx,
		envName,
		serviceImageEnvironmentKey(serviceConfig.GetName()),
		imageName,
	); err != nil {
		return nil, err
	}
	return response.GetResult(), nil
}

func (t *serviceTarget) Deploy(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	serviceContext *azdext.ServiceContext,
	targetResource *azdext.TargetResource,
	progress azdext.ProgressReporter,
) (*azdext.ServiceDeployResult, error) {
	if targetResource == nil {
		return nil, validationError(
			"missing_target_resource",
			fmt.Sprintf("service %q has no resolved Container App target", serviceConfig.GetName()),
			"run azd provision to restore the authored infrastructure outputs",
		)
	}
	projectResponse, err := t.client.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, fmt.Errorf("getting the azd project for service deployment: %w", err)
	}
	project := projectResponse.GetProject()
	if project == nil {
		return nil, validationError(
			"missing_project_config",
			"project configuration is unavailable during service deployment",
			"run the command from an initialized azd project",
		)
	}
	envName, values, err := t.runtime.environments.values(ctx)
	if err != nil {
		return nil, err
	}
	imageName, err := publishedContainerImage(serviceContext, values, serviceConfig.GetName())
	if err != nil {
		return nil, err
	}
	deploymentValues := make(map[string]string, len(values)+2)
	maps.Copy(deploymentValues, values)
	deploymentValues["AZURE_ENV_NAME"] = envName
	deploymentValues[serviceImageEnvironmentKey(serviceConfig.GetName())] = imageName

	result, err := t.modules.Deploy(ctx, &moduleDeploymentRequest{
		SubscriptionID:   targetResource.GetSubscriptionId(),
		ResourceGroup:    targetResource.GetResourceGroupName(),
		ServiceName:      serviceConfig.GetName(),
		Project:          project,
		Service:          serviceConfig,
		EnvironmentName:  envName,
		EnvironmentValue: deploymentValues,
		ImageName:        imageName,
	}, progress)
	if err != nil {
		return nil, err
	}
	resolvedValues := make(map[string]string, len(values)+len(result.Outputs)+1)
	maps.Copy(resolvedValues, values)
	delete(resolvedValues, containerAppIDEnv)
	if resourceID := targetResource.GetMetadata()["resourceId"]; resourceID != "" {
		resolvedValues[containerAppIDEnv] = resourceID
	}
	resolvedValues["AZURE_ENV_NAME"] = envName
	maps.Copy(resolvedValues, result.Outputs)
	deployedTarget, err := t.resources.Resolve(
		ctx,
		targetResource.GetSubscriptionId(),
		envName,
		resolvedValues,
		serviceConfig,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("resolving the deployed Container App: %w", err)
	}
	for key, value := range result.Outputs {
		if err := t.runtime.environments.set(ctx, envName, key, value); err != nil {
			return nil, fmt.Errorf("saving service deployment output %s: %w", key, err)
		}
	}

	progress("Waiting for Spring Boot service readiness")
	if err := t.resources.WaitReady(ctx, deployedTarget); err != nil {
		return nil, err
	}
	endpoints, err := t.resources.Endpoints(ctx, deployedTarget)
	if err != nil {
		return nil, err
	}
	artifacts := []*azdext.Artifact{{
		Kind:         azdext.ArtifactKind_ARTIFACT_KIND_RESOURCE,
		Location:     deployedTarget.GetMetadata()["resourceId"],
		LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
		Metadata: map[string]string{
			"subscriptionId":    deployedTarget.GetSubscriptionId(),
			"resourceGroupName": deployedTarget.GetResourceGroupName(),
			"resourceName":      deployedTarget.GetResourceName(),
			"resourceType":      deployedTarget.GetResourceType(),
		},
	}}
	for _, endpoint := range endpoints {
		artifacts = append(artifacts, &azdext.Artifact{
			Kind:         azdext.ArtifactKind_ARTIFACT_KIND_ENDPOINT,
			Location:     endpoint,
			LocationKind: azdext.LocationKind_LOCATION_KIND_REMOTE,
			Metadata:     map[string]string{"service": serviceConfig.GetName()},
		})
	}
	return &azdext.ServiceDeployResult{Artifacts: artifacts}, nil
}

func (t *serviceTarget) Endpoints(
	ctx context.Context,
	serviceConfig *azdext.ServiceConfig,
	targetResource *azdext.TargetResource,
) ([]string, error) {
	if targetResource == nil {
		return nil, validationError(
			"missing_target_resource",
			fmt.Sprintf("service %q has no resolved Container App target", serviceConfig.GetName()),
			"run azd provision to restore the authored infrastructure outputs",
		)
	}
	return t.resources.Endpoints(ctx, targetResource)
}

func serviceImageEnvironmentKey(serviceName string) string {
	return fmt.Sprintf("SERVICE_%s_IMAGE_NAME", environment.Key(serviceName))
}

func remoteContainerImage(artifacts []*azdext.Artifact) (string, error) {
	imageName := ""
	for _, artifact := range artifacts {
		if artifact == nil ||
			artifact.GetKind() != azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER ||
			artifact.GetLocationKind() != azdext.LocationKind_LOCATION_KIND_REMOTE {
			continue
		}
		location := strings.TrimSpace(artifact.GetLocation())
		if location == "" {
			location = strings.TrimSpace(artifact.GetMetadata()["remoteImage"])
		}
		if location == "" {
			continue
		}
		if imageName != "" && imageName != location {
			return "", validationError(
				"ambiguous_published_container",
				"container publish returned more than one remote image",
				"remove duplicate published container artifacts and retry",
			)
		}
		imageName = location
	}
	if imageName == "" {
		return "", validationError(
			"missing_published_container",
			"container publish returned no remote image",
			"verify the target registry and retry azd deploy",
		)
	}
	return imageName, nil
}

func publishedContainerImage(
	serviceContext *azdext.ServiceContext,
	values map[string]string,
	serviceName string,
) (string, error) {
	if serviceContext != nil && len(serviceContext.GetPublish()) > 0 {
		return remoteContainerImage(serviceContext.GetPublish())
	}
	if imageName := strings.TrimSpace(values[serviceImageEnvironmentKey(serviceName)]); imageName != "" {
		return imageName, nil
	}
	return "", validationError(
		"missing_published_container",
		"no published container image is available for service deployment",
		"run azd deploy without skipping package or publish",
	)
}
