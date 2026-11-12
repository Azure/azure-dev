// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azurecleanup

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
)

type subscriptionDeployment struct {
	name string
	tags map[string]*string
}

type subscriptionDeploymentsClient interface {
	list(ctx context.Context) ([]subscriptionDeployment, error)
	delete(ctx context.Context, name string) error
}

type armSubscriptionDeploymentsClient struct {
	client *armresources.DeploymentsClient
}

// CleanupSubscriptionDeployments requests deletion of subscription-scope ARM deployment records
// tagged with the supplied azd environment name. It does not wait for deletion to complete.
func CleanupSubscriptionDeployments(
	ctx context.Context,
	credential azcore.TokenCredential,
	subscriptionID string,
	environmentName string,
) error {
	client, err := armresources.NewDeploymentsClient(subscriptionID, credential, nil)
	if err != nil {
		return fmt.Errorf("create subscription deployments client: %w", err)
	}

	return cleanupSubscriptionDeployments(
		ctx,
		&armSubscriptionDeploymentsClient{client: client},
		environmentName,
	)
}

func cleanupSubscriptionDeployments(
	ctx context.Context,
	client subscriptionDeploymentsClient,
	environmentName string,
) error {
	deployments, err := client.list(ctx)
	if err != nil {
		return fmt.Errorf("list subscription deployments: %w", err)
	}

	var cleanupErrors []error
	for _, deployment := range deployments {
		if deployment.name == "" || !tagMatches(deployment.tags, azure.TagKeyAzdEnvName, environmentName) {
			continue
		}

		if err := client.delete(ctx, deployment.name); err != nil && !isNotFound(err) {
			cleanupErrors = append(
				cleanupErrors,
				fmt.Errorf("delete subscription deployment %q: %w", deployment.name, err),
			)
		}
	}

	return errors.Join(cleanupErrors...)
}

func (c *armSubscriptionDeploymentsClient) list(ctx context.Context) ([]subscriptionDeployment, error) {
	pager := c.client.NewListAtSubscriptionScopePager(nil)
	var deployments []subscriptionDeployment

	for pager.More() {
		response, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, deployment := range response.Value {
			if deployment == nil {
				continue
			}

			deployments = append(deployments, subscriptionDeployment{
				name: valueOrEmpty(deployment.Name),
				tags: deployment.Tags,
			})
		}
	}

	return deployments, nil
}

func (c *armSubscriptionDeploymentsClient) delete(ctx context.Context, name string) error {
	_, err := c.client.BeginDeleteAtSubscriptionScope(ctx, name, nil)
	return err
}

func tagMatches(tags map[string]*string, key string, expected string) bool {
	for tagKey, value := range tags {
		if strings.EqualFold(tagKey, key) && value != nil && *value == expected {
			return true
		}
	}

	return false
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}

func isNotFound(err error) bool {
	if responseErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		return responseErr.StatusCode == 404
	}

	return false
}
