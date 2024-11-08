package containerapps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/benbjohnson/clock"
	"github.com/braydonk/yaml"
)

const (
	pathLatestRevisionName                 = "properties.latestRevisionName"
	pathTemplate                           = "properties.template"
	pathTemplateRevisionSuffix             = "properties.template.revisionSuffix"
	pathTemplateContainers                 = "properties.template.containers"
	pathConfigurationActiveRevisionsMode   = "properties.configuration.activeRevisionsMode"
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
		options *ContainerAppOptions,
	) error
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

	if !shouldPersistDomains && !shouldPersistIngressSessionAffinity {
		return obj, nil
	}

	aca, err := cas.getContainerApp(ctx, subscriptionId, resourceGroupName, appName, options)
	if err != nil {
		log.Printf("failed getting current aca settings: %v. No settings will be persisted.", err)
		// if the container app doesn't exist, there's nothing for us to update in the desired state,
		// so we can just return the existing state as is.
		return obj, nil
	}

	objConfig := config.NewConfig(obj)

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
	options *ContainerAppOptions,
) error {
	containerApp, err := cas.getContainerApp(ctx, subscriptionId, resourceGroupName, appName, options)
	if err != nil {
		return fmt.Errorf("getting container app: %w", err)
	}

	// Get the latest revision name
	currentRevisionName, has := containerApp.GetString(pathLatestRevisionName)
	if !has {
		return fmt.Errorf("getting latest revision name: %w", err)
	}

	apiVersionPolicy := createApiVersionPolicy(options)
	revisionsClient, err := cas.createRevisionsClient(ctx, subscriptionId, apiVersionPolicy)
	if err != nil {
		return err
	}

	var revisionResponse *http.Response
	ctx = policy.WithCaptureResponse(ctx, &revisionResponse)

	if _, err := revisionsClient.GetRevision(ctx, resourceGroupName, appName, currentRevisionName, nil); err != nil {
		return fmt.Errorf("getting revision '%s': %w", currentRevisionName, err)
	}

	var revisionMap map[string]any
	if err := convert.FromHttpResponse(revisionResponse, &revisionMap); err != nil {
		return err
	}

	revision := config.NewConfig(revisionMap)

	// Update the revision with the new image name and suffix
	if err := revision.Set(pathTemplateRevisionSuffix, fmt.Sprintf("azd-%d", cas.clock.Now().Unix())); err != nil {
		return fmt.Errorf("setting revision suffix: %w", err)
	}

	var containers []map[string]any
	if ok, err := revision.GetSection(pathTemplateContainers, &containers); !ok || err != nil {
		return fmt.Errorf("getting containers: %w", err)
	}

	containers[0]["image"] = imageName
	if err := revision.Set(pathTemplateContainers, containers); err != nil {
		return fmt.Errorf("setting containers: %w", err)
	}

	// Update the container app with the new revision
	revisionTemplate, ok := revision.GetMap(pathTemplate)
	if !ok {
		return fmt.Errorf("getting revision template: %w", err)
	}

	if err := containerApp.Set(pathTemplate, revisionTemplate); err != nil {
		return fmt.Errorf("setting template: %w", err)
	}

	containerApp, err = cas.syncSecrets(ctx, subscriptionId, resourceGroupName, appName, containerApp)
	if err != nil {
		return fmt.Errorf("syncing secrets: %w", err)
	}

	// Update the container app
	err = cas.updateContainerApp(ctx, subscriptionId, resourceGroupName, appName, containerApp, options)
	if err != nil {
		return fmt.Errorf("updating container app revision: %w", err)
	}

	revisionMode, ok := containerApp.GetString(pathConfigurationActiveRevisionsMode)
	if !ok {
		return fmt.Errorf("getting active revisions mode: %w", err)
	}

	// If the container app is in multiple revision mode, update the traffic to point to the new revision
	if revisionMode == string(armappcontainers.ActiveRevisionsModeMultiple) {
		revisionSuffix, ok := revision.GetString(pathTemplateRevisionSuffix)
		if !ok {
			return fmt.Errorf("getting revision suffix: %w", err)
		}
		newRevisionName := fmt.Sprintf("%s--%s", appName, revisionSuffix)

		err = cas.setTrafficWeights(ctx, subscriptionId, resourceGroupName, appName, containerApp, newRevisionName, options)
		if err != nil {
			return fmt.Errorf("setting traffic weights: %w", err)
		}
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

func (cas *containerAppService) setTrafficWeights(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	containerApp config.Config,
	revisionName string,
	options *ContainerAppOptions,
) error {
	trafficWeights := []*armappcontainers.TrafficWeight{
		{
			RevisionName: &revisionName,
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

	err = cas.updateContainerApp(ctx, subscriptionId, resourceGroupName, appName, containerApp, options)
	if err != nil {
		return fmt.Errorf("updating traffic weights: %w", err)
	}

	return nil
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
		return nil, fmt.Errorf("getting container app: %w", err)
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
			return fmt.Errorf("failed to unmarshal container app: %w", err)
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

	return client, nil
}

func (cas *containerAppService) createRevisionsClient(
	ctx context.Context,
	subscriptionId string,
	customPolicy *containerAppCustomApiVersionAndBodyPolicy,
) (*armappcontainers.ContainerAppsRevisionsClient, error) {
	credential, err := cas.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := *cas.armClientOptions

	if customPolicy != nil {
		// Clone the options so we don't modify the original - we don't want to inject this custom policy into every request.
		options.PerCallPolicies = append(slices.Clone(options.PerCallPolicies), customPolicy)
	}

	client, err := armappcontainers.NewContainerAppsRevisionsClient(subscriptionId, credential, &options)
	if err != nil {
		return nil, fmt.Errorf("creating ContainerApps client: %w", err)
	}

	return client, nil
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
