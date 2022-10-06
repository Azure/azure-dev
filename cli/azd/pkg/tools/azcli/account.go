package azcli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
)

type AzCliSubscriptionInfo struct {
	Name      string `json:"name"`
	Id        string `json:"id"`
	TenantId  string `json:"tenantId"`
	IsDefault bool   `json:"isDefault"`
}

type AzCliLocation struct {
	// The human friendly name of the location (e.g. "West US 2")
	DisplayName string `json:"displayName"`
	// The name of the location (e.g. "westus2")
	Name string `json:"name"`
	// The human friendly name of the location, prefixed with a
	// region name (e.g "(US) West US 2")
	RegionalDisplayName string `json:"regionalDisplayName"`
}

func (cli *azCli) ListAccounts(ctx context.Context) ([]AzCliSubscriptionInfo, error) {
	client, err := cli.createSubscriptionsClient(ctx)
	if err != nil {
		return nil, err
	}

	var rawResponse *http.Response
	ctx = runtime.WithCaptureResponse(ctx, &rawResponse)

	subscriptions := []AzCliSubscriptionInfo{}
	pager := client.NewListPager(nil)

	for pager.More() {
		_, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed getting next page of subscriptions: %w", err)
		}

		// Using custom response processing unto Go SDK support required properties
		listResponse, err := readRawResponse[CustomListSubscriptionResponse](rawResponse)
		if err != nil {
			return nil, err
		}

		for _, subscription := range listResponse.Value {
			subscriptions = append(subscriptions, AzCliSubscriptionInfo{
				Id:       subscription.SubscriptionID,
				Name:     subscription.DisplayName,
				TenantId: subscription.TenantID,
			})
		}
	}

	sort.Slice(subscriptions, func(i, j int) bool {
		return subscriptions[i].Name < subscriptions[j].Name
	})

	return subscriptions, nil
}

func (cli *azCli) GetDefaultAccount(ctx context.Context) (*AzCliSubscriptionInfo, error) {
	result, err := cli.runAzCommand(
		ctx,
		"account", "show",
		"--output", "json",
	)

	if err != nil {
		return nil, fmt.Errorf("failed getting default account from az cli: %w", err)
	}

	var subscription AzCliSubscriptionInfo
	err = json.Unmarshal([]byte(result.Stdout), &subscription)
	if err != nil {
		return nil, fmt.Errorf("failed unmarshalling result JSON: %w", err)
	}

	return &subscription, nil
}

func (cli *azCli) GetAccount(ctx context.Context, subscriptionId string) (*AzCliSubscriptionInfo, error) {
	client, err := cli.createSubscriptionsClient(ctx)
	if err != nil {
		return nil, err
	}

	var rawResponse *http.Response
	ctx = runtime.WithCaptureResponse(ctx, &rawResponse)

	_, err = client.Get(ctx, subscriptionId, nil)
	if err != nil {
		return nil, fmt.Errorf("failed getting subscription for '%s'", subscriptionId)
	}

	// Using custom response processing unto Go SDK support required properties
	subscription, err := readRawResponse[CustomSubscription](rawResponse)
	if err != nil {
		return nil, err
	}

	return &AzCliSubscriptionInfo{
		Id:       subscription.SubscriptionID,
		Name:     subscription.DisplayName,
		TenantId: subscription.TenantID,
	}, nil
}

func (cli *azCli) GetSubscriptionTenant(ctx context.Context, subscriptionId string) (string, error) {
	subscription, err := cli.GetAccount(ctx, subscriptionId)
	if err != nil {
		return "", err
	}

	return subscription.TenantId, nil
}

func (cli *azCli) ListAccountLocations(ctx context.Context, subscriptionId string) ([]AzCliLocation, error) {
	client, err := cli.createSubscriptionsClient(ctx)
	if err != nil {
		return nil, err
	}

	locations := []AzCliLocation{}
	pager := client.NewListLocationsPager(subscriptionId, nil)

	var rawResponse *http.Response
	ctx = runtime.WithCaptureResponse(ctx, &rawResponse)

	for pager.More() {
		_, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed getting next page of locations: %w", err)
		}

		// Using custom response processing unto Go SDK support required properties
		locationResponse, err := readRawResponse[CustomListLocationsResponse](rawResponse)
		if err != nil {
			return nil, err
		}

		for _, location := range locationResponse.Value {
			// Ignore non-physical locations
			if location.Metadata.RegionType != "Physical" {
				continue
			}

			locations = append(locations, AzCliLocation{
				Name:                location.Name,
				DisplayName:         location.DisplayName,
				RegionalDisplayName: location.RegionalDisplayName,
			})
		}
	}

	sort.Slice(locations, func(i, j int) bool {
		return locations[i].RegionalDisplayName < locations[j].RegionalDisplayName
	})

	return locations, nil
}

func (cli *azCli) createSubscriptionsClient(ctx context.Context) (*armsubscription.SubscriptionsClient, error) {
	cred, err := identity.GetCredentials(ctx)
	if err != nil {
		return nil, err
	}

	// Uses latest api version of subscriptions api to get additional properties
	options := cli.createArmClientOptions(ctx, convert.RefOf("2020-01-01"))
	client, err := armsubscription.NewSubscriptionsClient(cred, options)
	if err != nil {
		return nil, fmt.Errorf("creating Subscriptions client: %w", err)
	}

	return client, nil
}

// Temporary custom response until Go SDK support all of the specified properties in the response
type CustomListLocationsResponse struct {
	Value []*CustomLocation `json:"value"`
}

// Temporary custom response until Go SDK support all of the specified properties in the response
// https://github.com/Azure/azure-sdk-for-go/issues/19241
type CustomLocation struct {
	ID                  string                 `json:"id"`
	Name                string                 `json:"name"`
	DisplayName         string                 `json:"displayName"`
	RegionalDisplayName string                 `json:"regionalDisplayName"` // Missing from Go SDK
	Metadata            CustomLocationMetadata `json:"metadata"`            // Missing from Go SDK
}

// Temporary custom response until Go SDK support all of the specified properties in the response
type CustomLocationMetadata struct {
	RegionType string `json:"regionType"`
}

// Temporary custom response until Go SDK support all of the specified properties in the response
type CustomListSubscriptionResponse struct {
	Value []*CustomSubscription `json:"value"`
}

// Temporary custom response until Go SDK support all of the specified properties in the response
// https://github.com/Azure/azure-sdk-for-go/issues/19243
type CustomSubscription struct {
	ID             string `json:"id"`
	SubscriptionID string `json:"subscriptionId"`
	TenantID       string `json:"tenantId"` // Missing from Go SDK
	DisplayName    string `json:"displayName"`
}
