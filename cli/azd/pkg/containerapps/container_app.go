// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package containerapps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/syncmap"
	"github.com/benbjohnson/clock"
	"github.com/braydonk/yaml"
)

const (
	pathLatestRevisionName                 = "properties.latestRevisionName"
	pathTemplate                           = "properties.template"
	pathTemplateRevisionSuffix             = "properties.template.revisionSuffix"
	pathTemplateContainers                 = "properties.template.containers"
	pathConfigurationActiveRevisionsMode   = "properties.configuration.activeRevisionsMode"
	pathConfigurationDapr                  = "properties.configuration.dapr"
	pathConfigurationSecrets               = "properties.configuration.secrets"
	pathConfigurationIngressTraffic        = "properties.configuration.ingress.traffic"
	pathConfigurationIngressFqdn           = "properties.configuration.ingress.fqdn"
	pathConfigurationIngressCustomDomains  = "properties.configuration.ingress.customDomains"
	pathConfigurationIngressStickySessions = "properties.configuration.ingress.stickySessions"
)

// ContainerAppService exposes operations for managing Azure Container Apps
type ContainerAppService interface {
	// Gets the ingress configuration for the specified container app
	GetIngressConfiguration(
		ctx context.Context,
		subscriptionId,
		resourceGroup,
		appName string,
		options *ContainerAppOptions,
	) (*ContainerAppIngressConfiguration, error)
	DeployYaml(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		appName string,
		containerAppYaml []byte,
		options *ContainerAppOptions,
	) error
	// Adds and activates a new revision to the specified container app
	AddRevision(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		appName string,
		imageName string,
		envVars map[string]string,
		options *ContainerAppOptions,
	) error
	// GetContainerAppJob gets a Container App Job by name
	GetContainerAppJob(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		jobName string,
		options *ContainerAppOptions,
	) (*armappcontainers.Job, error)
	// UpdateContainerAppJobImage updates the container image
	// and environment variables for a Container App Job
	UpdateContainerAppJobImage(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		jobName string,
		imageName string,
		envVars map[string]string,
		options *ContainerAppOptions,
	) error
	// GetLogStream returns a streaming reader for Container App console logs.
	// It discovers the latest revision and replica, obtains an auth token, and
	// connects to the replica container's log stream endpoint.
	// The caller is responsible for closing the returned reader.
	GetLogStream(
		ctx context.Context,
		subscriptionId string,
		resourceGroup string,
		appName string,
	) (io.ReadCloser, error)
}

// NewContainerAppService creates a new ContainerAppService
func NewContainerAppService(
	credentialProvider account.SubscriptionCredentialProvider,
	clock clock.Clock,
	armClientOptions *arm.ClientOptions,
	alphaFeatureManager *alpha.FeatureManager,
) ContainerAppService {
	return &containerAppService{
		credentialProvider:  credentialProvider,
		clock:               clock,
		armClientOptions:    armClientOptions,
		alphaFeatureManager: alphaFeatureManager,
	}
}

type containerAppService struct {
	credentialProvider  account.SubscriptionCredentialProvider
	clock               clock.Clock
	armClientOptions    *arm.ClientOptions
	alphaFeatureManager *alpha.FeatureManager
	appsClientCache     syncmap.Map[string, *armappcontainers.ContainerAppsClient]
	jobsClientCache     syncmap.Map[string, *armappcontainers.JobsClient]
}

type ContainerAppOptions struct {
	ApiVersion string
}

type ContainerAppIngressConfiguration struct {
	HostNames []string
}

// Gets the ingress configuration for the specified container app
func (cas *containerAppService) GetIngressConfiguration(
	ctx context.Context,
	subscriptionId string,
	resourceGroup string,
	appName string,
	options *ContainerAppOptions,
) (*ContainerAppIngressConfiguration, error) {
	containerApp, err := cas.getContainerApp(ctx, subscriptionId, resourceGroup, appName, options)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving container app properties: %w", err)
	}

	var hostNames []string
	fqdn, has := containerApp.GetString(pathConfigurationIngressFqdn)
	if has {
		hostNames = []string{fqdn}
	} else {
		hostNames = []string{}
	}

	return &ContainerAppIngressConfiguration{
		HostNames: hostNames,
	}, nil
}

// apiVersionKey is the key that can be set in the root of a deployment yaml to control the API version used when creating
// or updating the container app. When unset, we use the default API version of the armappcontainers.ContainerAppsClient.
const apiVersionKey = "api-version"

var persistCustomDomainsFeature = alpha.MustFeatureKey("aca.persistDomains")
var persistIngressSessionAffinity = alpha.MustFeatureKey("aca.persistIngressSessionAffinity")

func (cas *containerAppService) persistSettings(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	obj map[string]any,
	options *ContainerAppOptions,
) (map[string]any, error) {
	shouldPersistDomains := cas.alphaFeatureManager.IsEnabled(persistCustomDomainsFeature)
	shouldPersistIngressSessionAffinity := cas.alphaFeatureManager.IsEnabled(persistIngressSessionAffinity)

	// Preserve existing Dapr configuration when the deployment YAML does not include it.
	// This prevents Dapr config set externally (e.g. via Terraform) from being removed on deploy.
	objConfig := config.NewConfig(obj)
	_, hasDaprConfig := objConfig.Get(pathConfigurationDapr)
	shouldPreserveDapr := !hasDaprConfig

	if !shouldPersistDomains && !shouldPersistIngressSessionAffinity && !shouldPreserveDapr {
		return obj, nil
	}

	aca, err := cas.getContainerApp(ctx, subscriptionId, resourceGroupName, appName, options)
	if err != nil {
		// On first deploy the app doesn't exist yet (404) — proceed without persisting.
		var respErr *azcore.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == http.StatusNotFound {
			return obj, nil
		}

		// For alpha-gated features, preserve existing behavior: log and continue.
		// For Dapr preservation (correctness-critical), fail to prevent silent config wipe.
		if shouldPreserveDapr {
			return nil, fmt.Errorf("fetching existing container app to preserve Dapr config: %w", err)
		}

		log.Printf("failed getting current aca settings: %v. No settings will be persisted.", err)
		return obj, nil
	}

	if shouldPersistDomains {
		customDomains, has := aca.GetSlice(pathConfigurationIngressCustomDomains)
		if has {
			if err := objConfig.Set(pathConfigurationIngressCustomDomains, customDomains); err != nil {
				return nil, fmt.Errorf("setting custom domains: %w", err)
			}
		}
	}

	if shouldPersistIngressSessionAffinity {
		stickySessions, has := aca.Get(pathConfigurationIngressStickySessions)
		if has {
			if err := objConfig.Set(pathConfigurationIngressStickySessions, stickySessions); err != nil {
				return nil, fmt.Errorf("setting sticky sessions: %w", err)
			}
		}
	}

	if shouldPreserveDapr {
		daprConfig, has := aca.Get(pathConfigurationDapr)
		if has {
			if err := objConfig.Set(pathConfigurationDapr, daprConfig); err != nil {
				return nil, fmt.Errorf("setting dapr configuration: %w", err)
			}
		}
	}

	return objConfig.Raw(), nil
}

func (cas *containerAppService) DeployYaml(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	containerAppYaml []byte,
	options *ContainerAppOptions,
) error {
	var obj map[string]any
	if err := yaml.Unmarshal(containerAppYaml, &obj); err != nil {
		return fmt.Errorf("decoding yaml: %w", err)
	}

	obj, err := cas.persistSettings(ctx, subscriptionId, resourceGroupName, appName, obj, options)
	if err != nil {
		return fmt.Errorf("persisting aca settings: %w", err)
	}

	var poller *runtime.Poller[armappcontainers.ContainerAppsClientCreateOrUpdateResponse]

	// The way we make the initial request depends on whether the apiVersion is specified in the YAML.
	if apiVersion, ok := obj[apiVersionKey].(string); ok {
		// When the apiVersion is specified, we need to use a custom policy to inject the apiVersion and body into the
		// request. This is because the ContainerAppsClient is built for a specific api version and does not allow us to
		// change it.  The custom policy allows us to use the parts of the SDK around building the request URL and using
		// the standard pipeline - but we have to use a policy to change the api-version header and inject the body since
		// the armappcontainers.ContainerApp{} is also built for a specific api version.
		customPolicy := &containerAppCustomApiVersionAndBodyPolicy{
			apiVersion: apiVersion,
		}

		appClient, err := cas.createContainerAppsClient(ctx, subscriptionId, customPolicy)
		if err != nil {
			return err
		}

		// Remove the apiVersion field from the object so it doesn't get injected into the request body. On the wire this
		// is in a query parameter, not the body.
		delete(obj, apiVersionKey)

		containerAppJson, err := json.Marshal(obj)
		if err != nil {
			panic("should not have failed")
		}

		// Set the body injected by the policy to be the full container app JSON from the YAML.
		customPolicy.body = (*json.RawMessage)(&containerAppJson)

		// It doesn't matter what we configure here - the value is going to be overwritten by the custom policy. But we need
		// to pass in a value, so use the zero value.
		emptyApp := armappcontainers.ContainerApp{}

		p, err := appClient.BeginCreateOrUpdate(ctx, resourceGroupName, appName, emptyApp, nil)
		if err != nil {
			return fmt.Errorf("applying manifest: %w", err)
		}
		poller = p
	} else {
		// When the apiVersion field is unset in the YAML, we can use the standard SDK to build the request and send it
		// like normal.
		appClient, err := cas.createContainerAppsClient(ctx, subscriptionId, nil)
		if err != nil {
			return err
		}

		containerAppJson, err := json.Marshal(obj)
		if err != nil {
			panic("should not have failed")
		}

		var containerApp armappcontainers.ContainerApp
		if err := json.Unmarshal(containerAppJson, &containerApp); err != nil {
			return fmt.Errorf("converting to container app type: %w", err)
		}

		p, err := appClient.BeginCreateOrUpdate(ctx, resourceGroupName, appName, containerApp, nil)
		if err != nil {
			return fmt.Errorf("applying manifest: %w", err)
		}

		poller = p
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("polling for container app update completion: %w", err)
	}

	return nil
}

// Adds and activates a new revision to the specified container app
func (cas *containerAppService) AddRevision(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	imageName string,
	envVars map[string]string,
	options *ContainerAppOptions,
) error {
	containerApp, err := cas.getContainerApp(ctx, subscriptionId, resourceGroupName, appName, options)
	if err != nil {
		return fmt.Errorf("getting container app: %w", err)
	}

	// Update the template with the new image name and suffix
	if err := containerApp.Set(pathTemplateRevisionSuffix, fmt.Sprintf("azd-%d", cas.clock.Now().Unix())); err != nil {
		return fmt.Errorf("setting revision suffix: %w", err)
	}

	var containers []map[string]any
	if ok, err := containerApp.GetSection(pathTemplateContainers, &containers); !ok || err != nil {
		return fmt.Errorf("getting containers: %w", err)
	}

	containers[0]["image"] = imageName

	// Merge environment variables if provided
	if len(envVars) > 0 {
		// Get existing env vars from the container
		existingEnv, _ := containers[0]["env"].([]any)
		envMap := make(map[string]any)

		// Build a map from existing env vars
		for _, envItem := range existingEnv {
			if envEntry, ok := envItem.(map[string]any); ok {
				if name, ok := envEntry["name"].(string); ok {
					envMap[name] = envEntry
				}
			}
		}

		// Merge new env vars (these will override existing ones with the same name)
		for key, value := range envVars {
			envMap[key] = map[string]any{
				"name":  key,
				"value": value,
			}
		}

		// Convert back to array
		mergedEnv := make([]any, 0, len(envMap))
		for _, envEntry := range envMap {
			mergedEnv = append(mergedEnv, envEntry)
		}

		containers[0]["env"] = mergedEnv
	}

	if err := containerApp.Set(pathTemplateContainers, containers); err != nil {
		return fmt.Errorf("setting containers: %w", err)
	}

	containerApp, err = cas.syncSecrets(ctx, subscriptionId, resourceGroupName, appName, containerApp)
	if err != nil {
		return fmt.Errorf("syncing secrets: %w", err)
	}

	revisionMode, ok := containerApp.GetString(pathConfigurationActiveRevisionsMode)
	if !ok {
		return fmt.Errorf("container app is missing active revisions mode configuration")
	}

	// If the container app is in multiple revision mode, update the traffic to point to the new revision.
	if revisionMode == string(armappcontainers.ActiveRevisionsModeMultiple) {
		revisionSuffix, _ := containerApp.GetString(pathTemplateRevisionSuffix)
		newRevisionName := fmt.Sprintf("%s--%s", appName, revisionSuffix)

		trafficWeights := []*armappcontainers.TrafficWeight{
			{
				RevisionName: &newRevisionName,
				Weight:       to.Ptr[int32](100),
			},
		}

		trafficWeightsJson, err := convert.ToJsonArray(trafficWeights)
		if err != nil {
			return fmt.Errorf("converting traffic weights to JSON: %w", err)
		}
		if err := containerApp.Set(pathConfigurationIngressTraffic, trafficWeightsJson); err != nil {
			return fmt.Errorf("setting traffic weights: %w", err)
		}
	}

	err = cas.updateContainerApp(ctx, subscriptionId, resourceGroupName, appName, containerApp, options)
	if err != nil {
		return fmt.Errorf("updating container app revision: %w", err)
	}

	return nil
}

func (cas *containerAppService) syncSecrets(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	containerApp config.Config,
) (config.Config, error) {
	// If the container app doesn't have any existingSecrets, we don't need to do anything
	existingSecrets, ok := containerApp.GetSlice(pathConfigurationSecrets)
	if !ok || len(existingSecrets) == 0 {
		return containerApp, nil
	}

	appClient, err := cas.createContainerAppsClient(ctx, subscriptionId, nil)
	if err != nil {
		return nil, err
	}

	// Copy the secret configuration from the current version
	// Secret values are not returned by the API, so we need to get them separately
	// to ensure the update call succeeds
	secretsResponse, err := appClient.ListSecrets(ctx, resourceGroupName, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("listing secrets: %w", err)
	}

	secrets := secretsResponse.SecretsCollection.Value
	secretsJson, err := convert.ToJsonArray(secrets)
	if err != nil {
		return nil, err
	}

	err = containerApp.Set(pathConfigurationSecrets, secretsJson)
	if err != nil {
		return nil, fmt.Errorf("setting secrets: %w", err)
	}

	return containerApp, nil
}

func (cas *containerAppService) getContainerApp(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	options *ContainerAppOptions,
) (config.Config, error) {
	apiVersionPolicy := createApiVersionPolicy(options)

	appClient, err := cas.createContainerAppsClient(ctx, subscriptionId, apiVersionPolicy)
	if err != nil {
		return nil, err
	}

	var res *http.Response
	ctx = policy.WithCaptureResponse(ctx, &res)

	_, err = appClient.Get(ctx, resourceGroupName, appName, nil)
	if err != nil {
		err = fmt.Errorf("getting container app: %w", err)
		if strings.Contains(err.Error(), "unmarshalling type") {
			err = withApiVersionSuggestion(err)
		}
		return nil, err
	}

	var containAppMap map[string]any
	err = convert.FromHttpResponse(res, &containAppMap)
	if err != nil {
		return nil, err
	}

	containAppConfig := config.NewConfig(containAppMap)

	return containAppConfig, nil
}

func (cas *containerAppService) updateContainerApp(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	containerApp config.Config,
	options *ContainerAppOptions,
) error {
	containerAppJson, err := json.Marshal(containerApp.Raw())
	if err != nil {
		return fmt.Errorf("marshalling container app: %w", err)
	}

	apiVersionPolicy := createApiVersionPolicy(options)
	if apiVersionPolicy != nil {
		apiVersionPolicy.body = (*json.RawMessage)(&containerAppJson)
	}

	appClient, err := cas.createContainerAppsClient(ctx, subscriptionId, apiVersionPolicy)
	if err != nil {
		return err
	}

	// This container app BODY will be replaced by the custom policy when configured
	var containerAppResource armappcontainers.ContainerApp
	if apiVersionPolicy == nil {
		if err := json.Unmarshal(containerAppJson, &containerAppResource); err != nil {
			return withApiVersionSuggestion(fmt.Errorf("failed to unmarshal container app: %w", err))
		}
	}

	poller, err := appClient.BeginUpdate(ctx, resourceGroupName, appName, containerAppResource, nil)
	if err != nil {
		return fmt.Errorf("begin updating ingress traffic: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("polling for container app update completion: %w", err)
	}

	return nil
}

func (cas *containerAppService) createContainerAppsClient(
	ctx context.Context,
	subscriptionId string,
	customPolicy *containerAppCustomApiVersionAndBodyPolicy,
) (*armappcontainers.ContainerAppsClient, error) {
	if customPolicy == nil {
		if cachedClient, ok := cas.appsClientCache.Load(subscriptionId); ok {
			return cachedClient, nil
		}
	}

	credential, err := cas.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := *cas.armClientOptions

	if customPolicy != nil {
		// Clone the options so we don't modify the original - we don't want to inject this custom policy into every request.
		options.PerCallPolicies = append(slices.Clone(options.PerCallPolicies), customPolicy)
	}

	client, err := armappcontainers.NewContainerAppsClient(subscriptionId, credential, &options)
	if err != nil {
		return nil, fmt.Errorf("creating ContainerApps client: %w", err)
	}

	if customPolicy == nil {
		if cachedClient, loaded := cas.appsClientCache.LoadOrStore(subscriptionId, client); loaded {
			return cachedClient, nil
		}
	}

	return client, nil
}

func (cas *containerAppService) createJobsClient(
	ctx context.Context,
	subscriptionId string,
	customPolicy *containerAppCustomApiVersionAndBodyPolicy,
) (*armappcontainers.JobsClient, error) {
	if customPolicy == nil {
		if cached, ok := cas.jobsClientCache.Load(subscriptionId); ok {
			return cached, nil
		}
	}

	credential, err := cas.credentialProvider.CredentialForSubscription(
		ctx, subscriptionId,
	)
	if err != nil {
		return nil, err
	}

	options := *cas.armClientOptions

	if customPolicy != nil {
		// Clone the options so we don't modify the original -
		// we don't want to inject this custom policy into
		// every request.
		options.PerCallPolicies = append(
			slices.Clone(options.PerCallPolicies), customPolicy,
		)
	}

	client, err := armappcontainers.NewJobsClient(
		subscriptionId, credential, &options,
	)
	if err != nil {
		return nil, fmt.Errorf("creating Jobs client: %w", err)
	}

	if customPolicy == nil {
		if cached, loaded := cas.jobsClientCache.LoadOrStore(
			subscriptionId, client,
		); loaded {
			return cached, nil
		}
	}

	return client, nil
}

func (cas *containerAppService) GetContainerAppJob(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	jobName string,
	options *ContainerAppOptions,
) (*armappcontainers.Job, error) {
	apiVersionPolicy := createApiVersionPolicy(options)

	jobsClient, err := cas.createJobsClient(
		ctx, subscriptionId, apiVersionPolicy,
	)
	if err != nil {
		return nil, err
	}

	response, err := jobsClient.Get(
		ctx, resourceGroupName, jobName, nil,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"getting container app job: %w", err,
		)
	}

	return &response.Job, nil
}

func (cas *containerAppService) UpdateContainerAppJobImage(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	jobName string,
	imageName string,
	envVars map[string]string,
	options *ContainerAppOptions,
) error {
	if imageName == "" {
		return fmt.Errorf(
			"image name must not be empty for container app job %s",
			jobName,
		)
	}

	apiVersionPolicy := createApiVersionPolicy(options)

	jobsClient, err := cas.createJobsClient(
		ctx, subscriptionId, apiVersionPolicy,
	)
	if err != nil {
		return fmt.Errorf("creating jobs client: %w", err)
	}

	// Get current job using the already-created client
	getResp, err := jobsClient.Get(
		ctx, resourceGroupName, jobName, nil,
	)
	if err != nil {
		return fmt.Errorf(
			"getting container app job for update: %w", err,
		)
	}
	job := &getResp.Job

	// Find the target container by matching the image repository.
	// The new imageName typically shares the same repository prefix (e.g.,
	// "registry.azurecr.io/app:newtag"). If no container matches by repository,
	// fall back to the first container (standard azd convention).
	if job.Properties == nil ||
		job.Properties.Template == nil ||
		len(job.Properties.Template.Containers) == 0 {
		return fmt.Errorf(
			"container app job %s has no containers to update",
			jobName,
		)
	}

	targetIdx := 0
	newRepo := imageRepository(imageName)
	if newRepo != "" {
		for i, c := range job.Properties.Template.Containers {
			if c != nil && c.Image != nil && imageRepository(*c.Image) == newRepo {
				targetIdx = i
				break
			}
		}
	}

	container := job.Properties.Template.Containers[targetIdx]
	if container == nil {
		return fmt.Errorf(
			"container app job %s has a nil container entry",
			jobName,
		)
	}
	container.Image = &imageName

	// Merge environment variables if provided
	if len(envVars) > 0 {
		envMap := make(map[string]*armappcontainers.EnvironmentVar)

		// Build map from existing env vars
		for _, env := range container.Env {
			if env != nil && env.Name != nil {
				envMap[*env.Name] = env
			}
		}

		// Merge new env vars (override existing with same name)
		for key, value := range envVars {
			envMap[key] = &armappcontainers.EnvironmentVar{
				Name:  new(key),
				Value: new(value),
			}
		}

		// Convert back to slice
		merged := make([]*armappcontainers.EnvironmentVar, 0, len(envMap))
		for _, env := range envMap {
			merged = append(merged, env)
		}
		container.Env = merged
	}

	// Patch the job with the updated template
	jobPatch := armappcontainers.JobPatchProperties{
		Properties: &armappcontainers.JobPatchPropertiesProperties{
			Template: job.Properties.Template,
		},
	}

	poller, err := jobsClient.BeginUpdate(
		ctx, resourceGroupName, jobName, jobPatch, nil,
	)
	if err != nil {
		return fmt.Errorf(
			"updating container app job image: %w", err,
		)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf(
			"waiting for container app job update: %w", err,
		)
	}

	return nil
}

type containerAppCustomApiVersionAndBodyPolicy struct {
	apiVersion string
	body       *json.RawMessage
}

func (p *containerAppCustomApiVersionAndBodyPolicy) Do(req *policy.Request) (*http.Response, error) {
	if p.apiVersion != "" {
		log.Printf("setting api-version to %s", p.apiVersion)

		reqQP := req.Raw().URL.Query()
		reqQP.Set("api-version", p.apiVersion)
		req.Raw().URL.RawQuery = reqQP.Encode()
	}

	if p.body != nil {
		log.Printf("setting body to %s", string(*p.body))

		if err := req.SetBody(streaming.NopCloser(bytes.NewReader(*p.body)), "application/json"); err != nil {
			return nil, fmt.Errorf("updating request body: %w", err)
		}

		// Reset the body on the policy so it doesn't get reused on the next request
		p.body = nil
	}

	return req.Next()
}

func createApiVersionPolicy(options *ContainerAppOptions) *containerAppCustomApiVersionAndBodyPolicy {
	if options == nil || options.ApiVersion == "" {
		return nil
	}

	return &containerAppCustomApiVersionAndBodyPolicy{
		apiVersion: options.ApiVersion,
	}
}

func withApiVersionSuggestion(err error) error {
	suggestion := "Suggestion: set 'apiVersion' on your service in azure.yaml to match the API version " +
		"in your IaC:\n\n" +
		"services:\n" +
		"  your-service:\n" +
		output.WithSuccessFormat("    apiVersion: 2025-02-02-preview")

	return &internal.ErrorWithSuggestion{
		Err:        err,
		Suggestion: suggestion,
	}
}

// imageRepository extracts the repository portion of a container image reference
// by stripping the tag or digest suffix. For example:
//   - "registry.azurecr.io/app:v2" → "registry.azurecr.io/app"
//   - "registry.azurecr.io/app@sha256:abc" → "registry.azurecr.io/app"
//   - "registry.azurecr.io/app" → "registry.azurecr.io/app"
//
// Returns "" if the image string is empty.
func imageRepository(image string) string {
	if image == "" {
		return ""
	}
	// Strip digest suffix first (@sha256:...), then tag suffix (:tag).
	if idx := strings.LastIndex(image, "@"); idx > 0 {
		return image[:idx]
	}
	// For tags, only strip after the last "/" to avoid stripping port numbers
	// (e.g., "registry:5000/app:v1" should become "registry:5000/app").
	lastSlash := strings.LastIndex(image, "/")
	if idx := strings.LastIndex(image, ":"); idx > lastSlash {
		return image[:idx]
	}
	return image
}
