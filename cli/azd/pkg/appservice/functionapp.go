package appservice

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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/benbjohnson/clock"
	"gopkg.in/yaml.v3"
)

// apiVersionKey is the key that can be set in the root of a deployment yaml to control the API version used when creating
// or updating the app service app. When unset, we use the default API version of the armappservice.WevAppsClient.
const apiVersionKey = "api-version"

type FunctionAppService struct {
	credentialProvider account.SubscriptionCredentialProvider
	transport          policy.Transporter
	clock              clock.Clock
	armClientOptions   *arm.ClientOptions
}

func NewFunctionAppService(
	credentialProvider account.SubscriptionCredentialProvider,
	transport policy.Transporter,
	clock clock.Clock,
	armClientOptions *arm.ClientOptions,
) *FunctionAppService {
	return &FunctionAppService{
		credentialProvider: credentialProvider,
		transport:          transport,
		clock:              clock,
		armClientOptions:   armClientOptions,
	}
}

func (fas *FunctionAppService) DeployYAML(ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
	containerAppYaml []byte) error {

	var obj map[string]any
	if err := yaml.Unmarshal(containerAppYaml, &obj); err != nil {
		return fmt.Errorf("decoding yaml: %w", err)
	}

	var poller *runtime.Poller[armappservice.WebAppsClientCreateOrUpdateResponse]

	// The way we make the initial request depends on whether the apiVersion is specified in the YAML.
	if apiVersion, ok := obj[apiVersionKey].(string); ok {
		// When the apiVersion is specified, we need to use a custom policy to inject the apiVersion and body into the
		// request. This is because the ContainerAppsClient is built for a specific api version and does not allow us to
		// change it.  The custom policy allows us to use the parts of the SDK around building the request URL and using
		// the standard pipeline - but we have to use a policy to change the api-version header and inject the body since
		// the armappcontainers.ContainerApp{} is also built for a specific api version.
		customPolicy := &customApiVersionAndBodyPolicy{
			apiVersion: apiVersion,
		}

		appClient, err := fas.createWebAppsClientWithPolicy(ctx, subscriptionId, customPolicy)
		if err != nil {
			return err
		}

		// Remove the apiVersion field from the object so it doesn't get injected into the request body. On the wire this
		// is in a query parameter, not the body.
		delete(obj, apiVersionKey)

		functionAppJson, err := json.Marshal(obj)
		if err != nil {
			panic("should not have failed")
		}

		// Set the body injected by the policy to be the full container app JSON from the YAML.
		customPolicy.body = (*json.RawMessage)(&functionAppJson)

		// It doesn't matter what we configure here - the value is going to be overwritten by the custom policy. But we need
		// to pass in a value, so use the zero value.
		emptyApp := armappservice.Site{}

		p, err := appClient.BeginCreateOrUpdate(ctx, resourceGroupName, appName, emptyApp, nil)
		if err != nil {
			return fmt.Errorf("applying manifest: %w", err)
		}
		poller = p

		// Now that we've sent the request, clear the body so it is not injected on any subsequent requests (e.g. ones made
		// by the poller when we poll).
		customPolicy.body = nil
	} else {
		// When the apiVersion field is unset in the YAML, we can use the standard SDK to build the request and send it
		// like normal.
		appClient, err := fas.createWebAppsClient(ctx, subscriptionId)
		if err != nil {
			return err
		}

		containerAppJson, err := json.Marshal(obj)
		if err != nil {
			panic("should not have failed")
		}

		var site armappservice.Site
		if err := json.Unmarshal(containerAppJson, &site); err != nil {
			return fmt.Errorf("converting to container app type: %w", err)
		}

		p, err := appClient.BeginCreateOrUpdate(ctx, resourceGroupName, appName, site, nil)
		if err != nil {
			return fmt.Errorf("applying manifest: %w", err)
		}

		poller = p
	}

	_, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("polling for container app update completion: %w", err)
	}

	return nil
}

func (fas *FunctionAppService) Endpoints(ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	appName string,
) ([]string, error) {
	appClient, err := fas.createWebAppsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	res, err := appClient.Get(ctx, resourceGroupName, appName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting web app: %w", err)
	}

	var endpoints []string
	for _, hostName := range res.Properties.HostNames {
		if hostName != nil && *hostName != "" {
			endpoints = append(endpoints, fmt.Sprintf("https://%s/", *hostName))
		}
	}

	return endpoints, nil
}

func (fas *FunctionAppService) createWebAppsClient(
	ctx context.Context,
	subscriptionId string,
) (*armappservice.WebAppsClient, error) {
	credential, err := fas.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armappservice.NewWebAppsClient(subscriptionId, credential, fas.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating ContainerApps client: %w", err)
	}

	return client, nil
}

func (fas *FunctionAppService) createWebAppsClientWithPolicy(
	ctx context.Context,
	subscriptionId string,
	policy policy.Policy,
) (*armappservice.WebAppsClient, error) {
	credential, err := fas.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	// Clone the options so we don't modify the original - we don't want to inject this custom policy into every request.
	options := *fas.armClientOptions
	options.PerCallPolicies = append(slices.Clone(options.PerCallPolicies), policy)

	client, err := armappservice.NewWebAppsClient(subscriptionId, credential, &options)
	if err != nil {
		return nil, fmt.Errorf("creating WebApps client: %w", err)
	}

	return client, nil
}

type customApiVersionAndBodyPolicy struct {
	apiVersion string
	body       *json.RawMessage
}

func (p *customApiVersionAndBodyPolicy) Do(req *policy.Request) (*http.Response, error) {
	if p.body != nil {
		reqQP := req.Raw().URL.Query()
		reqQP.Set("api-version", p.apiVersion)
		req.Raw().URL.RawQuery = reqQP.Encode()

		log.Printf("setting body to %s", string(*p.body))

		if err := req.SetBody(streaming.NopCloser(bytes.NewReader(*p.body)), "application/json"); err != nil {
			return nil, fmt.Errorf("updating request body: %w", err)
		}
	}

	return req.Next()
}
