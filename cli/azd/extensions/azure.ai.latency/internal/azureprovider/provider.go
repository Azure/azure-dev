// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package azureprovider loads Azure OpenAI deployment metadata, telemetry, and
// live offer eligibility from Azure management and monitoring APIs.
package azureprovider

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"azure.ai.latency/internal/insights"
	"azure.ai.latency/internal/model"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

var (
	_ insights.TrafficProfileProvider   = (*Provider)(nil)
	_ insights.OfferEligibilityProvider = (*Provider)(nil)
)

// Provider implements the live Azure data sources used by latency assessments.
type Provider struct {
	subscriptionID string
	cognitive      cognitiveClient
	metricsFactory func(string) (metricsClient, error)
	logs           logsClient
}

// New creates a live Azure latency provider for a resolved subscription.
//
// The credential should be created for the user's access tenant. In extension
// command code, use azdext.NewTokenProvider with Subscription.UserTenantId (or
// the tenant returned by LookupTenant).
func New(subscriptionID string, credential azcore.TokenCredential) (*Provider, error) {
	subscriptionID = strings.TrimSpace(subscriptionID)
	if subscriptionID == "" {
		return nil, fmt.Errorf("subscription ID is required")
	}
	if credential == nil {
		return nil, fmt.Errorf("Azure credential is required")
	}

	cognitive, err := newSDKCognitiveClient(subscriptionID, credential)
	if err != nil {
		return nil, safeServiceError("create Cognitive Services client", err)
	}
	logs, err := newSDKLogsClient(credential)
	if err != nil {
		return nil, safeServiceError("create Azure Monitor Logs client", err)
	}

	return &Provider{
		subscriptionID: subscriptionID,
		cognitive:      cognitive,
		logs:           logs,
		metricsFactory: func(endpoint string) (metricsClient, error) {
			client, clientErr := newSDKMetricsClient(endpoint, credential)
			if clientErr != nil {
				return nil, safeServiceError("create Azure Monitor Metrics client", clientErr)
			}
			return client, nil
		},
	}, nil
}

type cognitiveClient interface {
	GetDeploymentMetadata(
		context.Context,
		string,
		string,
		string,
	) (deploymentMetadata, error)
	ListModels(context.Context, string) ([]regionalModel, error)
	ListModelCapacities(context.Context, string, string, string) ([]modelCapacity, error)
	ListUsages(context.Context, string) ([]quotaUsage, error)
}

type metricsClient interface {
	Query(context.Context, metricQuery) (metricQueryResult, error)
}

type logsClient interface {
	Query(context.Context, logQuery) (logQueryResult, error)
}

type deploymentMetadata struct {
	ID           string
	ModelName    string
	ModelVersion string
	SKUName      string
	ServiceTier  string
	Region       string
	RateLimits   []rateLimit
}

type rateLimit struct {
	Key           string
	Count         float64
	PeriodSeconds float64
}

type regionalModel struct {
	Name         string
	Version      string
	Format       string
	Capabilities map[string]string
	SKUs         []regionalSKU
}

type regionalSKU struct {
	Name      string
	UsageName string
	Minimum   *int
}

type modelCapacity struct {
	Region            string
	SKUName           string
	AvailableCapacity *float64
}

type quotaUsage struct {
	Name    string
	Current *float64
	Limit   *float64
}

type metricAggregation string

const (
	metricAggregationTotal   metricAggregation = "Total"
	metricAggregationAverage metricAggregation = "Average"
)

type metricQuery struct {
	SubscriptionID string
	ResourceID     string
	DeploymentName string
	Names          []string
	Aggregation    metricAggregation
	Start          time.Time
	End            time.Time
}

type metricPoint struct {
	Time  time.Time
	Value float64
}

type metricQueryResult struct {
	Series  map[string][]metricPoint
	Partial bool
}

type logQuery struct {
	ResourceID string
	Text       string
	Start      time.Time
	End        time.Time
}

type logQueryResult struct {
	Rows    []map[string]any
	Partial bool
}

type observedValues struct {
	numeric    map[string]float64
	apiPath    string
	requestIDs []string
}

func newObservedValues() observedValues {
	return observedValues{numeric: map[string]float64{}}
}

func (v *observedValues) overlay(other observedValues) {
	maps.Copy(v.numeric, other.numeric)
	if other.apiPath != "" {
		v.apiPath = other.apiPath
	}
	if len(other.requestIDs) > 0 {
		v.requestIDs = other.requestIDs
	}
}

func (v observedValues) number(key string) *float64 {
	value, ok := v.numeric[key]
	if !ok {
		return nil
	}
	return model.Float64(value)
}
