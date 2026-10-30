// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// MonitoringConnectionRecord is one connection returned by a list call.
type MonitoringConnectionRecord struct {
	Name       string
	Properties *armcognitiveservices.ConnectionPropertiesV2
}

// MonitoringReader is the read-only Azure surface used by monitoring discovery
// and validation. Implementations must return sanitized errors and must report
// found=false / exists=false only for a genuine 404.
type MonitoringReader interface {
	Resource(ctx context.Context, id *arm.ResourceID, version string) (*armresources.GenericResource, bool, error)
	ProjectConnections(ctx context.Context, project *arm.ResourceID) ([]MonitoringConnectionRecord, error)
	AccountConnections(ctx context.Context, project *arm.ResourceID) ([]MonitoringConnectionRecord, error)
	ProjectConnectionExists(ctx context.Context, project *arm.ResourceID, name string) (bool, error)
}

// MonitoringResourceID validates the exact ARM resource type and scope.
func MonitoringResourceID(value, resourceType string) (*arm.ResourceID, error) {
	id, err := arm.ParseResourceID(value)
	if err != nil || id.SubscriptionID == "" || id.ResourceGroupName == "" ||
		!strings.EqualFold(id.ResourceType.String(), resourceType) {
		return nil, fmt.Errorf("monitoring requires a %s resource ID", resourceType)
	}
	return id, nil
}

// MonitoringReadError sanitizes SDK errors, whose bodies can contain credentials.
func MonitoringReadError(operation string, err error) error {
	for _, cancellation := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, cancellation) {
			return fmt.Errorf("%s: %w", operation, cancellation)
		}
	}
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok {
		return fmt.Errorf("%s failed (HTTP %d); verify resource access and retry", operation, response.StatusCode)
	}
	return fmt.Errorf("%s failed; verify resource access and retry", operation)
}

func monitoringNotFound(err error) bool {
	response, ok := errors.AsType[*azcore.ResponseError](err)
	return ok && response.StatusCode == 404
}

// armMonitoringReader is the SDK-backed MonitoringReader. It is the only place
// that constructs Azure SDK clients for monitoring discovery.
type armMonitoringReader struct {
	credential azcore.TokenCredential
	options    *arm.ClientOptions
}

func (r *armMonitoringReader) Resource(
	ctx context.Context, id *arm.ResourceID, version string,
) (*armresources.GenericResource, bool, error) {
	client, err := armresources.NewClient(id.SubscriptionID, r.credential, r.options)
	if err != nil {
		return nil, false, err
	}
	response, err := client.GetByID(ctx, id.String(), version, nil)
	if monitoringNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, MonitoringReadError("read monitoring resource", err)
	}
	return &response.GenericResource, true, nil
}

func (r *armMonitoringReader) ProjectConnections(
	ctx context.Context, project *arm.ResourceID,
) ([]MonitoringConnectionRecord, error) {
	factory, err := armcognitiveservices.NewClientFactory(project.SubscriptionID, r.credential, r.options)
	if err != nil {
		return nil, err
	}
	pager := factory.NewProjectConnectionsClient().NewListPager(
		project.ResourceGroupName, project.Parent.Name, project.Name, nil)
	var records []MonitoringConnectionRecord
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, MonitoringReadError("inspect project monitoring connections", err)
		}
		if page.Value == nil {
			return nil, fmt.Errorf("project connection discovery returned an incomplete list")
		}
		for _, connection := range page.Value {
			if connection == nil || connection.Name == nil || *connection.Name == "" || connection.Properties == nil {
				return nil, fmt.Errorf("project connection discovery returned incomplete metadata")
			}
			records = append(records, MonitoringConnectionRecord{
				Name: *connection.Name, Properties: connection.Properties.GetConnectionPropertiesV2(),
			})
		}
	}
	return records, nil
}

func (r *armMonitoringReader) AccountConnections(
	ctx context.Context, project *arm.ResourceID,
) ([]MonitoringConnectionRecord, error) {
	factory, err := armcognitiveservices.NewClientFactory(project.SubscriptionID, r.credential, r.options)
	if err != nil {
		return nil, err
	}
	pager := factory.NewAccountConnectionsClient().NewListPager(project.ResourceGroupName, project.Parent.Name, nil)
	var records []MonitoringConnectionRecord
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, MonitoringReadError("inspect inherited monitoring connections", err)
		}
		if page.Value == nil {
			return nil, fmt.Errorf("account connection discovery returned an incomplete list")
		}
		for _, connection := range page.Value {
			if connection == nil || connection.Name == nil || *connection.Name == "" || connection.Properties == nil {
				return nil, fmt.Errorf("account connection discovery returned incomplete metadata")
			}
			records = append(records, MonitoringConnectionRecord{
				Name: *connection.Name, Properties: connection.Properties.GetConnectionPropertiesV2(),
			})
		}
	}
	return records, nil
}

func (r *armMonitoringReader) ProjectConnectionExists(
	ctx context.Context, project *arm.ResourceID, name string,
) (bool, error) {
	factory, err := armcognitiveservices.NewClientFactory(project.SubscriptionID, r.credential, r.options)
	if err != nil {
		return false, err
	}
	_, err = factory.NewProjectConnectionsClient().Get(
		ctx, project.ResourceGroupName, project.Parent.Name, project.Name, name, nil)
	if err == nil {
		return true, nil
	}
	if monitoringNotFound(err) {
		return false, nil
	}
	return false, MonitoringReadError("confirm system monitoring connection name is available", err)
}
