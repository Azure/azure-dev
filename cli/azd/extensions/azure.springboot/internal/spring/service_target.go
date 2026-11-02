// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	azdexec "github.com/azure/azure-dev/cli/azd/pkg/exec"
	"google.golang.org/protobuf/proto"
)

type serviceTarget struct {
	azdext.BaseServiceTargetProvider
	client          *azdext.AzdClient
	runtime         *Runtime
	modules         moduleDeployer
	resources       containerAppResourceService
	images          runtimeImageBuilder
	moduleMu        sync.Mutex
	resolvedModules map[string]*resolvedServiceModule
}

// NewServiceTarget creates the Spring Boot service target.
func NewServiceTarget(client *azdext.AzdClient, runtime *Runtime) azdext.ServiceTargetProvider {
	return newServiceTarget(
		client, runtime, newAzureModuleDeployer(client, io.Discard), newContainerAppResourceService(client),
	)
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
		images: newRuntimeImageBuilder(azdexec.NewCommandRunner(&azdexec.RunnerOptions{
			Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard,
		})),
	}
}

func (t *serviceTarget) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	t.moduleMu.Lock()
	t.resolvedModules = nil
	t.moduleMu.Unlock()
	projectResponse, err := t.client.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("getting the azd project for Spring service validation: %w", err)
	}
	if err := validateSpringBuild(projectResponse.GetProject(), serviceConfig); err != nil {
		return err
	}
	modules, err := resolveProjectServiceModules(ctx, t.client, projectResponse.GetProject())
	if err != nil {
		return err
	}
	t.moduleMu.Lock()
	t.resolvedModules = modules
	t.moduleMu.Unlock()
	return t.runtime.ValidateService(ctx, serviceConfig, projectResponse.GetProject())
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
	if err := validateAutomaticServiceConfig(serviceConfig); err != nil {
		return nil, err
	}
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
	if _, err := t.runtime.readServiceDatasource(ctx, project, serviceConfig, serviceContext, false); err != nil {
		return nil, err
	}
	for _, artifacts := range [][]*azdext.Artifact{serviceContext.GetBuild(), serviceContext.GetPackage()} {
		for _, artifact := range artifacts {
			if artifact.GetKind() == azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER {
				return nil, validationError("ambiguous_native_container_artifacts",
					"native Maven packaging cannot reuse an unrelated container build or package artifact",
					"rerun azd package with only this service's current Maven package artifacts")
			}
		}
	}

	staged, err := prepareContainerContext(ctx, project, serviceConfig, serviceContext)
	if err != nil {
		return nil, err
	}

	progress("Building Spring Boot container")
	buildArtifact, buildErr := t.images.Build(ctx, staged)
	cleanupErr := staged.cleanup()
	buildErr = nonDisclosureError(buildErr)
	if buildErr != nil || cleanupErr != nil {
		return nil, errors.Join(buildErr, cleanupErr)
	}
	if buildArtifact == nil {
		return nil, validationError(
			"missing_container_build_artifact",
			"the container build returned no artifacts",
			"verify Docker is running and the generated Dockerfile is included in the build context",
		)
	}
	buildContext := proto.Clone(serviceContext).(*azdext.ServiceContext)
	buildContext.Build = append(buildContext.Build, buildArtifact)

	progress("Packaging Spring Boot container")
	packageResponse, err := t.client.Container().Package(ctx, &azdext.ContainerPackageRequest{
		ServiceName:    serviceConfig.GetName(),
		ServiceContext: buildContext,
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
	buildContext.Package = append(buildContext.Package, packageResponse.GetResult().GetArtifacts()...)
	if err := validateCoreContainerPackage(buildContext); err != nil {
		return nil, err
	}
	serviceContext.Build = append(serviceContext.Build, buildArtifact)
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
	t.moduleMu.Lock()
	module := t.resolvedModules[serviceConfig.GetName()]
	t.moduleMu.Unlock()
	if module == nil {
		modules, err := resolveProjectServiceModules(ctx, t.client, project)
		if err != nil {
			return nil, err
		}
		module = modules[serviceConfig.GetName()]
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
		Module:           module,
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
	progress("Waiting for Spring Boot service readiness")
	if err := t.resources.WaitReady(ctx, deployedTarget); err != nil {
		return nil, err
	}
	endpoints, err := t.resources.Endpoints(ctx, deployedTarget)
	if err != nil {
		return nil, err
	}
	endpoints, err = selectServiceEndpoints(serviceConfig.GetName(), endpoints, result.Outputs)
	if err != nil {
		return nil, err
	}
	if err := persistDeploymentOutputs(ctx, t.runtime.environments, envName, result.Outputs); err != nil {
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
	endpoints, err := t.resources.Endpoints(ctx, targetResource)
	if err != nil {
		return nil, err
	}
	_, values, err := t.runtime.environments.values(ctx)
	if err != nil {
		return nil, err
	}
	return selectServiceEndpoints(serviceConfig.GetName(), endpoints, values)
}

func validateCoreContainerPackage(serviceContext *azdext.ServiceContext) error {
	var image *azdext.Artifact
	for _, artifact := range serviceContext.GetPackage() {
		if artifact.GetKind() != azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER {
			continue
		}
		if image != nil {
			return validationError("ambiguous_container_package", "more than one container package artifact was supplied",
				"rerun azd package with only this service's core Docker package artifacts")
		}
		image = artifact
	}
	if image == nil || image.GetLocationKind() != azdext.LocationKind_LOCATION_KIND_LOCAL ||
		strings.TrimSpace(image.GetLocation()) == "" || image.GetMetadata()["targetImage"] != image.GetLocation() ||
		image.GetMetadata()["imageHash"] == "" || image.GetMetadata()["sourceImage"] != "" {
		return validationError("missing_core_container_package",
			"native Maven packaging requires a local container package artifact",
			"rerun azd package so the generated Spring Boot container is built and packaged")
	}
	for _, artifact := range serviceContext.GetBuild() {
		if artifact.GetKind() == azdext.ArtifactKind_ARTIFACT_KIND_CONTAINER &&
			artifact.GetLocationKind() == azdext.LocationKind_LOCATION_KIND_LOCAL &&
			artifact.GetMetadata()["framework"] == "docker" &&
			artifact.GetLocation() == image.GetMetadata()["imageHash"] {
			return nil
		}
	}
	return validationError("mismatched_core_container_package",
		"the container package does not match this service's core Docker build artifact",
		"rerun azd package without reusing stale artifacts")
}

func selectServiceEndpoints(serviceName string, armEndpoints []string, outputs map[string]string) ([]string, error) {
	var authored string
	found := false
	prefix := "SERVICE_" + environment.Key(serviceName)
	for _, suffix := range []string{"_URI", "_URL", "_ENDPOINT"} {
		value, present := outputs[prefix+suffix]
		if !present {
			continue
		}
		if found && authored != value {
			return nil, validationError("conflicting_service_endpoints", "service endpoint outputs conflict",
				"author one consistent HTTPS service endpoint for the deployed Container App")
		}
		authored, found = value, true
	}
	if !found {
		return armEndpoints, nil
	}
	endpoint, err := url.Parse(authored)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.Opaque != "" ||
		(endpoint.Port() != "" && endpoint.Port() != "443") {
		return nil, validationError("invalid_service_endpoint",
			"the authored service endpoint must be HTTPS without credentials, query parameters, or fragments",
			"use the deployed Container App hostname and preserve the application context path")
	}
	for _, origin := range armEndpoints {
		live, err := url.Parse(origin)
		if err == nil && live.Scheme == "https" && strings.EqualFold(live.Hostname(), endpoint.Hostname()) {
			return []string{authored}, nil
		}
	}
	return nil, validationError("foreign_service_endpoint",
		"the authored service endpoint does not belong to the deployed app",
		"use the hostname returned by the verified live Container App")
}

func persistDeploymentOutputs(
	ctx context.Context,
	environments environmentStore,
	envName string,
	outputs map[string]string,
) error {
	for key, value := range outputs {
		if err := environments.set(ctx, envName, key, value); err != nil {
			return fmt.Errorf("saving service deployment output %s: %w", key, err)
		}
	}
	return nil
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
