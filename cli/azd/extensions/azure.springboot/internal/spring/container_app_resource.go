// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	containerAppNameEnv = "AZURE_CONTAINER_APP_NAME"
	containerAppType    = "Microsoft.App/containerApps"
	azdServiceNameTag   = "azd-service-name"
)

type containerAppResourceService interface {
	Resolve(
		context.Context,
		string,
		string,
		map[string]string,
		*azdext.ServiceConfig,
		func() (*azdext.TargetResource, error),
	) (*azdext.TargetResource, error)
	Endpoints(context.Context, *azdext.TargetResource) ([]string, error)
	WaitReady(context.Context, *azdext.TargetResource) error
}

type containerAppSnapshot struct {
	ID                      string
	Name                    string
	Type                    string
	Location                string
	Tags                    map[string]*string
	Fqdn                    string
	LatestRevisionName      string
	LatestReadyRevisionName string
	ProvisioningState       string
}

type containerAppReader interface {
	Get(context.Context, string, string) (*containerAppSnapshot, error)
}

type azureContainerAppResourceService struct {
	client       *azdext.AzdClient
	reader       func(context.Context, string) (containerAppReader, error)
	pollInterval time.Duration
	readyTimeout time.Duration
}

func newContainerAppResourceService(client *azdext.AzdClient) containerAppResourceService {
	service := &azureContainerAppResourceService{
		client:       client,
		pollInterval: 5 * time.Second,
		readyTimeout: 10 * time.Minute,
	}
	service.reader = service.newReader
	return service
}

func (s *azureContainerAppResourceService) Resolve(
	ctx context.Context,
	subscriptionID string,
	envName string,
	values map[string]string,
	service *azdext.ServiceConfig,
	defaultResolver func() (*azdext.TargetResource, error),
) (*azdext.TargetResource, error) {
	if strings.TrimSpace(subscriptionID) == "" {
		subscriptionID = strings.TrimSpace(values[azureSubscriptionIDEnv])
	}
	if subscriptionID == "" {
		return nil, validationError(
			"missing_subscription_id",
			"the selected Azure subscription is unavailable",
			"restore AZURE_SUBSCRIPTION_ID in the azd environment",
		)
	}

	candidate, err := targetFromPersistedIdentity(subscriptionID, values)
	if err != nil {
		return nil, err
	}
	var defaultErr error
	if candidate == nil && defaultResolver != nil {
		var defaultTarget *azdext.TargetResource
		defaultTarget, defaultErr = defaultResolver()
		if defaultErr == nil {
			candidate = defaultTarget
		}
	}
	if candidate == nil {
		resourceGroup := strings.TrimSpace(values[azureResourceGroupEnv])
		resourceName := strings.TrimSpace(values[containerAppNameEnv])
		if resourceGroup != "" && resourceName != "" {
			candidate = &azdext.TargetResource{
				SubscriptionId:    subscriptionID,
				ResourceGroupName: resourceGroup,
				ResourceName:      resourceName,
				ResourceType:      containerAppType,
			}
		}
	}
	if candidate == nil {
		if defaultErr != nil {
			return nil, fmt.Errorf("resolving the default Container App target: %w", nonDisclosureError(defaultErr))
		}
		return nil, validationError(
			"missing_container_app_identity",
			fmt.Sprintf("service %q has no persisted Container App identity", service.GetName()),
			"run azd provision to restore the authored infrastructure outputs",
		)
	}
	if !strings.EqualFold(candidate.GetSubscriptionId(), subscriptionID) {
		return nil, validationError(
			"mismatched_container_app_subscription",
			"the persisted Container App identity does not match the selected subscription",
			"select the environment's original subscription",
		)
	}
	if candidate.GetResourceType() != "" && !strings.EqualFold(candidate.GetResourceType(), containerAppType) {
		return nil, validationError(
			"unsupported_target_resource",
			fmt.Sprintf("service %q resolved unsupported resource type %q", service.GetName(), candidate.GetResourceType()),
			"configure the service to use a Microsoft.App/containerApps resource",
		)
	}
	if candidate.GetResourceGroupName() == "" || candidate.GetResourceName() == "" {
		return nil, validationError(
			"incomplete_container_app_identity",
			"the persisted Container App identity is incomplete",
			"restore the authored infrastructure outputs and retry",
		)
	}

	reader, err := s.reader(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	app, err := reader.Get(ctx, candidate.GetResourceGroupName(), candidate.GetResourceName())
	if err != nil {
		return nil, fmt.Errorf("reading the target Container App: %w", nonDisclosureError(err))
	}
	if app.ID == "" || !strings.EqualFold(app.Type, containerAppType) ||
		!tagEqualsPointers(app.Tags, azdEnvironmentTag, envName) ||
		!tagEqualsPointers(app.Tags, azdServiceNameTag, service.GetName()) {
		return nil, validationError(
			"container_app_ownership_unverified",
			"the resolved Container App is not owned by the selected azd environment and service",
			"restore the authored deployment outputs or select the correct environment",
		)
	}
	return &azdext.TargetResource{
		SubscriptionId:    subscriptionID,
		ResourceGroupName: candidate.GetResourceGroupName(),
		ResourceName:      app.Name,
		ResourceType:      containerAppType,
		Metadata: map[string]string{
			"location":   app.Location,
			"resourceId": app.ID,
		},
	}, nil
}

func targetFromPersistedIdentity(
	subscriptionID string,
	values map[string]string,
) (*azdext.TargetResource, error) {
	resourceID := strings.TrimSpace(values[containerAppIDEnv])
	if resourceID == "" {
		return nil, nil
	}
	identity, err := arm.ParseResourceID(resourceID)
	if err != nil {
		return nil, validationError(
			"invalid_container_app_identity",
			"AZURE_CONTAINER_APP_ID is not a valid Azure resource ID",
			"restore the authored infrastructure output and retry",
		)
	}
	if !strings.EqualFold(identity.SubscriptionID, subscriptionID) ||
		!strings.EqualFold(identity.ResourceType.Namespace, "Microsoft.App") ||
		!strings.EqualFold(identity.ResourceType.Type, "containerApps") {
		return nil, validationError(
			"invalid_container_app_identity",
			"AZURE_CONTAINER_APP_ID does not identify a Container App in the selected subscription",
			"restore the authored infrastructure output and retry",
		)
	}
	return &azdext.TargetResource{
		SubscriptionId:    identity.SubscriptionID,
		ResourceGroupName: identity.ResourceGroupName,
		ResourceName:      identity.Name,
		ResourceType:      containerAppType,
		Metadata:          map[string]string{"resourceId": resourceID},
	}, nil
}

func (s *azureContainerAppResourceService) Endpoints(
	ctx context.Context,
	target *azdext.TargetResource,
) ([]string, error) {
	reader, err := s.reader(ctx, target.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	app, err := reader.Get(ctx, target.GetResourceGroupName(), target.GetResourceName())
	if err != nil {
		return nil, fmt.Errorf("reading Container App endpoint: %w", nonDisclosureError(err))
	}
	if strings.TrimSpace(app.Fqdn) == "" {
		return nil, validationError(
			"container_app_endpoint_missing",
			"the Container App has no ingress endpoint",
			"verify the authored service module enables external ingress",
		)
	}
	return []string{"https://" + strings.TrimSuffix(app.Fqdn, "/") + "/"}, nil
}

func (s *azureContainerAppResourceService) WaitReady(
	ctx context.Context,
	target *azdext.TargetResource,
) error {
	reader, err := s.reader(ctx, target.GetSubscriptionId())
	if err != nil {
		return err
	}
	readyContext, cancel := context.WithTimeout(ctx, s.readyTimeout)
	defer cancel()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		app, err := reader.Get(readyContext, target.GetResourceGroupName(), target.GetResourceName())
		if err != nil {
			return fmt.Errorf("reading Container App readiness: %w", err)
		}
		if strings.EqualFold(app.ProvisioningState, "Failed") {
			return errors.New("Container App provisioning failed")
		}
		if app.LatestRevisionName != "" && app.LatestReadyRevisionName == app.LatestRevisionName {
			return nil
		}
		select {
		case <-readyContext.Done():
			return fmt.Errorf("waiting for Container App readiness: %w", readyContext.Err())
		case <-ticker.C:
		}
	}
}

func (s *azureContainerAppResourceService) newReader(
	ctx context.Context,
	subscriptionID string,
) (containerAppReader, error) {
	credential, err := credentialForSubscription(ctx, s.client, subscriptionID)
	if err != nil {
		return nil, err
	}
	client, err := armappcontainers.NewContainerAppsClient(subscriptionID, credential, nil)
	if err != nil {
		return nil, fmt.Errorf("creating Container Apps client: %w", err)
	}
	return &azureContainerAppReader{client: client}, nil
}

type azureContainerAppReader struct {
	client *armappcontainers.ContainerAppsClient
}

func (r *azureContainerAppReader) Get(
	ctx context.Context,
	resourceGroup string,
	resourceName string,
) (*containerAppSnapshot, error) {
	response, err := r.client.Get(ctx, resourceGroup, resourceName, nil)
	if err != nil {
		if responseErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
			responseErr.StatusCode == http.StatusNotFound {
			return nil, validationError(
				"container_app_not_found",
				"the persisted target Container App no longer exists",
				"run azd provision to restore the authored infrastructure",
			)
		}
		return nil, err
	}
	app := response.ContainerApp
	snapshot := &containerAppSnapshot{
		ID:       valueOrEmpty(app.ID),
		Name:     valueOrEmpty(app.Name),
		Type:     valueOrEmpty(app.Type),
		Location: valueOrEmpty(app.Location),
		Tags:     app.Tags,
	}
	if app.Properties != nil {
		snapshot.LatestRevisionName = valueOrEmpty(app.Properties.LatestRevisionName)
		snapshot.LatestReadyRevisionName = valueOrEmpty(app.Properties.LatestReadyRevisionName)
		if app.Properties.ProvisioningState != nil {
			snapshot.ProvisioningState = string(*app.Properties.ProvisioningState)
		}
		if app.Properties.Configuration != nil && app.Properties.Configuration.Ingress != nil {
			snapshot.Fqdn = valueOrEmpty(app.Properties.Configuration.Ingress.Fqdn)
		}
	}
	return snapshot, nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
