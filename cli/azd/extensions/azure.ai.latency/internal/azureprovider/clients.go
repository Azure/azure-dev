// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureprovider

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azlogs"
	"github.com/Azure/azure-sdk-for-go/sdk/monitor/query/azmetrics"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
)

type sdkCognitiveClient struct {
	accounts    *armcognitiveservices.AccountsClient
	deployments *armcognitiveservices.DeploymentsClient
	models      *armcognitiveservices.ModelsClient
	capacities  *armcognitiveservices.ModelCapacitiesClient
	usages      *armcognitiveservices.UsagesClient
}

func newSDKCognitiveClient(
	subscriptionID string,
	credential azcore.TokenCredential,
) (*sdkCognitiveClient, error) {
	factory, err := armcognitiveservices.NewClientFactory(subscriptionID, credential, nil)
	if err != nil {
		return nil, err
	}
	return &sdkCognitiveClient{
		accounts:    factory.NewAccountsClient(),
		deployments: factory.NewDeploymentsClient(),
		models:      factory.NewModelsClient(),
		capacities:  factory.NewModelCapacitiesClient(),
		usages:      factory.NewUsagesClient(),
	}, nil
}

func (c *sdkCognitiveClient) GetDeploymentMetadata(
	ctx context.Context,
	resourceGroup string,
	accountName string,
	deploymentName string,
) (deploymentMetadata, error) {
	deployment, err := c.deployments.Get(ctx, resourceGroup, accountName, deploymentName, nil)
	if err != nil {
		return deploymentMetadata{}, err
	}
	account, err := c.accounts.Get(ctx, resourceGroup, accountName, nil)
	if err != nil {
		return deploymentMetadata{}, err
	}

	result := deploymentMetadata{
		ID:     valueOrEmpty(deployment.ID),
		Region: valueOrEmpty(account.Location),
	}
	if deployment.SKU != nil {
		result.SKUName = valueOrEmpty(deployment.SKU.Name)
	}
	if deployment.Properties == nil {
		return result, nil
	}
	if deployment.Properties.Model != nil {
		result.ModelName = valueOrEmpty(deployment.Properties.Model.Name)
		result.ModelVersion = valueOrEmpty(deployment.Properties.Model.Version)
	}
	result.ServiceTier = mapValue(deployment.Properties.Capabilities, "serviceTier")
	result.RateLimits = appendRateLimits(result.RateLimits, deployment.Properties.RateLimits)
	if deployment.Properties.Model != nil && deployment.Properties.Model.CallRateLimit != nil {
		result.RateLimits = appendRateLimits(
			result.RateLimits,
			deployment.Properties.Model.CallRateLimit.Rules,
		)
	}
	return result, nil
}

func appendRateLimits(
	destination []rateLimit,
	source []*armcognitiveservices.ThrottlingRule,
) []rateLimit {
	for _, item := range source {
		if item == nil || item.Key == nil || item.Count == nil || item.RenewalPeriod == nil {
			continue
		}
		destination = append(destination, rateLimit{
			Key:           *item.Key,
			Count:         float64(*item.Count),
			PeriodSeconds: float64(*item.RenewalPeriod),
		})
	}
	return destination
}

func (c *sdkCognitiveClient) ListModels(ctx context.Context, region string) ([]regionalModel, error) {
	pager := c.models.NewListPager(region, nil)
	result := []regionalModel{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Value {
			if item == nil || item.Model == nil {
				continue
			}
			model := regionalModel{
				Name:         valueOrEmpty(item.Model.Name),
				Version:      valueOrEmpty(item.Model.Version),
				Format:       valueOrEmpty(item.Model.Format),
				Capabilities: stringMap(item.Model.Capabilities),
				SKUs:         make([]regionalSKU, 0, len(item.Model.SKUs)),
			}
			for _, sku := range item.Model.SKUs {
				if sku == nil {
					continue
				}
				var minimum *int
				if sku.Capacity != nil && sku.Capacity.Minimum != nil && *sku.Capacity.Minimum > 0 {
					minimum = new(int(*sku.Capacity.Minimum))
				}
				model.SKUs = append(model.SKUs, regionalSKU{
					Name:      valueOrEmpty(sku.Name),
					UsageName: valueOrEmpty(sku.UsageName),
					Minimum:   minimum,
				})
			}
			result = append(result, model)
		}
	}
	return result, nil
}

func (c *sdkCognitiveClient) ListModelCapacities(
	ctx context.Context,
	format string,
	name string,
	version string,
) ([]modelCapacity, error) {
	pager := c.capacities.NewListPager(format, name, version, nil)
	result := []modelCapacity{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Value {
			if item == nil || item.Properties == nil {
				continue
			}
			var available *float64
			if item.Properties.AvailableCapacity != nil {
				available = new(float64(*item.Properties.AvailableCapacity))
			}
			result = append(result, modelCapacity{
				Region:            valueOrEmpty(item.Location),
				SKUName:           valueOrEmpty(item.Properties.SKUName),
				AvailableCapacity: available,
			})
		}
	}
	return result, nil
}

func (c *sdkCognitiveClient) ListUsages(ctx context.Context, region string) ([]quotaUsage, error) {
	pager := c.usages.NewListPager(region, nil)
	result := []quotaUsage{}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Value {
			if item == nil {
				continue
			}
			usage := quotaUsage{
				Current: finitePointer(item.CurrentValue),
				Limit:   finitePointer(item.Limit),
			}
			if item.Name != nil {
				usage.Name = valueOrEmpty(item.Name.Value)
			}
			result = append(result, usage)
		}
	}
	return result, nil
}

type sdkMetricsClient struct {
	client *azmetrics.Client
}

func newSDKMetricsClient(endpoint string, credential azcore.TokenCredential) (*sdkMetricsClient, error) {
	client, err := azmetrics.NewClient(endpoint, credential, nil)
	if err != nil {
		return nil, err
	}
	return &sdkMetricsClient{client: client}, nil
}

func (c *sdkMetricsClient) Query(ctx context.Context, query metricQuery) (metricQueryResult, error) {
	aggregation := string(query.Aggregation)
	filter := fmt.Sprintf("ModelDeploymentName eq '%s'", escapeODataString(query.DeploymentName))
	interval := "PT1M"
	start := query.Start.UTC().Format(time.RFC3339)
	end := query.End.UTC().Format(time.RFC3339)
	response, err := c.client.QueryResources(
		ctx,
		query.SubscriptionID,
		"Microsoft.CognitiveServices/accounts",
		query.Names,
		azmetrics.ResourceIDList{ResourceIDs: []string{query.ResourceID}},
		&azmetrics.QueryResourcesOptions{
			Aggregation: &aggregation,
			EndTime:     &end,
			Filter:      &filter,
			Interval:    &interval,
			StartTime:   &start,
		},
	)
	if err != nil {
		return metricQueryResult{}, err
	}

	result := metricQueryResult{Series: map[string][]metricPoint{}}
	for _, resource := range response.Values {
		for _, metric := range resource.Values {
			name := ""
			if metric.Name != nil {
				name = valueOrEmpty(metric.Name.Value)
			}
			if name == "" {
				continue
			}
			if metric.ErrorCode != nil && !strings.EqualFold(*metric.ErrorCode, "Success") {
				result.Partial = true
				continue
			}
			result.Series[name] = mergeMetricTimeSeries(metric.TimeSeries, query.Aggregation)
		}
	}
	return result, nil
}

func mergeMetricTimeSeries(
	series []azmetrics.TimeSeriesElement,
	aggregation metricAggregation,
) []metricPoint {
	type bucket struct {
		sum   float64
		count int
	}
	buckets := map[time.Time]bucket{}
	withoutTimestamp := []metricPoint{}
	for _, timeSeries := range series {
		for _, point := range timeSeries.Data {
			value := point.Total
			if aggregation == metricAggregationAverage {
				value = point.Average
			}
			if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
				continue
			}
			if point.TimeStamp == nil {
				withoutTimestamp = append(withoutTimestamp, metricPoint{Value: *value})
				continue
			}
			timestamp := point.TimeStamp.UTC()
			current := buckets[timestamp]
			current.sum += *value
			current.count++
			buckets[timestamp] = current
		}
	}

	times := make([]time.Time, 0, len(buckets))
	for timestamp := range buckets {
		times = append(times, timestamp)
	}
	slices.SortFunc(times, func(left, right time.Time) int {
		return left.Compare(right)
	})
	result := make([]metricPoint, 0, len(times)+len(withoutTimestamp))
	for _, timestamp := range times {
		current := buckets[timestamp]
		value := current.sum
		if aggregation == metricAggregationAverage && current.count > 0 {
			value /= float64(current.count)
		}
		result = append(result, metricPoint{Time: timestamp, Value: value})
	}
	return append(result, withoutTimestamp...)
}

type sdkLogsClient struct {
	client *azlogs.Client
}

func newSDKLogsClient(credential azcore.TokenCredential) (*sdkLogsClient, error) {
	client, err := azlogs.NewClient(credential, nil)
	if err != nil {
		return nil, err
	}
	return &sdkLogsClient{client: client}, nil
}

func (c *sdkLogsClient) Query(ctx context.Context, query logQuery) (logQueryResult, error) {
	text := query.Text
	timespan := azlogs.NewTimeInterval(query.Start.UTC(), query.End.UTC())
	response, err := c.client.QueryResource(ctx, query.ResourceID, azlogs.QueryBody{
		Query:    &text,
		Timespan: &timespan,
	}, nil)
	if err != nil {
		return logQueryResult{}, err
	}

	result := logQueryResult{Partial: response.Error != nil}
	for _, table := range response.Tables {
		columns := make([]string, 0, len(table.Columns))
		for _, column := range table.Columns {
			columns = append(columns, valueOrEmpty(column.Name))
		}
		for _, row := range table.Rows {
			values := make(map[string]any, min(len(columns), len(row)))
			for index := range min(len(columns), len(row)) {
				if columns[index] != "" {
					values[columns[index]] = row[index]
				}
			}
			result.Rows = append(result.Rows, values)
		}
	}
	if response.Error != nil && len(result.Rows) == 0 {
		return logQueryResult{}, fmt.Errorf("Azure Monitor Logs returned no query data")
	}
	return result, nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func mapValue(values map[string]*string, key string) string {
	for name, value := range values {
		if strings.EqualFold(name, key) {
			return valueOrEmpty(value)
		}
	}
	return ""
}

func stringMap(values map[string]*string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = valueOrEmpty(value)
	}
	return result
}

func finitePointer(value *float64) *float64 {
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return nil
	}
	return new(*value)
}

func escapeODataString(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
