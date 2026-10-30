// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/google/uuid"
)

// MonitoringConnectionName matches ARM guid() and Terraform UUID v5, including
// ARM's published namespace. Hashing the project name stays within connection
// limits without truncating sibling names that share a long prefix.
func MonitoringConnectionName(projectName string) string {
	namespace := uuid.MustParse("11fb06fb-712d-4ddd-98c7-e71bbd588830")
	id := uuid.NewSHA1(namespace, []byte(strings.ToLower(projectName)))
	return "azd-appinsights-" + strings.ReplaceAll(id.String(), "-", "")[:13]
}

// MonitoringResolver performs explicit read-only monitoring discovery outside
// synthesis. Reader overrides the default SDK-backed reader, mainly for tests.
type MonitoringResolver struct {
	Credential    azcore.TokenCredential
	ClientOptions *arm.ClientOptions
	Reader        MonitoringReader
}

func (p *MonitoringResolver) reader() MonitoringReader {
	if p.Reader != nil {
		return p.Reader
	}
	return &armMonitoringReader{credential: p.Credential, options: p.ClientOptions}
}

// Resource distinguishes genuine absence from failed reads and verifies the returned identity.
func (p *MonitoringResolver) Resource(
	ctx context.Context, id *arm.ResourceID, version string,
) (*armresources.GenericResource, bool, error) {
	resource, found, err := p.reader().Resource(ctx, id, version)
	if err != nil || !found {
		return nil, false, err
	}
	if resource == nil || resource.ID == nil || !strings.EqualFold(*resource.ID, id.String()) {
		return nil, false, fmt.Errorf("monitoring lookup returned an unexpected resource identity")
	}
	return resource, true, nil
}

// ValidateComponent verifies a workspace-backed destination and, when needed, ApiKey policy.
func (p *MonitoringResolver) ValidateComponent(
	ctx context.Context, componentID string, needsKey bool,
) error {
	id, err := MonitoringResourceID(componentID, "Microsoft.Insights/components")
	if err != nil {
		return err
	}
	component, found, err := p.Resource(ctx, id, "2020-02-02")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("the selected Application Insights component does not exist")
	}
	properties, err := MonitoringProperties(component)
	if err != nil {
		return err
	}
	workspaceID, _ := properties["WorkspaceResourceId"].(string)
	workspace, err := MonitoringResourceID(workspaceID, "Microsoft.OperationalInsights/workspaces")
	if err != nil {
		return fmt.Errorf("Application Insights must reference an accessible Log Analytics workspace: %w", err)
	}
	linked, found, err := p.Resource(ctx, workspace, "2023-09-01")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("the linked Log Analytics workspace does not exist")
	}
	if needsKey {
		if err := MonitoringKeyPolicy(properties); err != nil {
			return err
		}
		if key, _ := properties["ConnectionString"].(string); strings.TrimSpace(key) == "" {
			return fmt.Errorf("the selected Application Insights component has no connection string")
		}
		workspaceProperties, err := MonitoringProperties(linked)
		if err != nil {
			return err
		}
		return MonitoringKeyPolicy(workspaceProperties)
	}
	return nil
}
