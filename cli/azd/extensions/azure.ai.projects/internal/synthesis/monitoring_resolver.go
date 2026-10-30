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

// MonitoringResolver performs explicit read-only monitoring discovery outside synthesis.
type MonitoringResolver struct {
	Credential    azcore.TokenCredential
	ClientOptions *arm.ClientOptions
}

// MonitoringConnection identifies an effective connection without its credentials.
type MonitoringConnection struct {
	Name   string
	Target string
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

// Resource distinguishes genuine absence from failed reads and verifies the returned identity.
func (p *MonitoringResolver) Resource(
	ctx context.Context, id *arm.ResourceID, version string,
) (*armresources.GenericResource, bool, error) {
	client, err := armresources.NewClient(id.SubscriptionID, p.Credential, p.ClientOptions)
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
	if response.ID == nil || !strings.EqualFold(*response.ID, id.String()) {
		return nil, false, fmt.Errorf("monitoring lookup returned an unexpected resource identity")
	}
	return &response.GenericResource, true, nil
}

// DiscoverConnection resolves effective project/inherited connections without fetching credentials.
func (p *MonitoringResolver) DiscoverConnection(
	ctx context.Context, project *arm.ResourceID, projectExists, accountExists bool,
) (*MonitoringConnection, error) {
	factory, err := armcognitiveservices.NewClientFactory(project.SubscriptionID, p.Credential, p.ClientOptions)
	if err != nil {
		return nil, err
	}
	reserved := MonitoringConnectionName(project.Name)
	projectConnections := map[string]*armcognitiveservices.ConnectionPropertiesV2{}
	var selected *MonitoringConnection
	add := func(name string, properties *armcognitiveservices.ConnectionPropertiesV2) error {
		if properties == nil || properties.Category == nil {
			return fmt.Errorf("connection properties are unavailable; monitoring discovery is incomplete")
		}
		isMonitoring := strings.EqualFold(string(*properties.Category), "AppInsights")
		if strings.EqualFold(name, reserved) && !isMonitoring {
			return fmt.Errorf("the system monitoring connection name is already in use; no connection was changed")
		}
		if !isMonitoring {
			return nil
		}
		if properties.Target == nil || properties.AuthType == nil {
			return fmt.Errorf("existing AppInsights connection lacks its target or authentication type")
		}
		if _, err := MonitoringResourceID(*properties.Target, "Microsoft.Insights/components"); err != nil {
			return err
		}
		if resourceID := properties.Metadata["ResourceId"]; resourceID != nil &&
			!strings.EqualFold(*resourceID, *properties.Target) {
			return fmt.Errorf("existing AppInsights target and resource metadata disagree")
		}
		auth := string(*properties.AuthType)
		if !strings.EqualFold(auth, "ApiKey") && !strings.EqualFold(auth, "AAD") &&
			!strings.EqualFold(auth, "ManagedIdentity") {
			return fmt.Errorf("existing AppInsights authentication cannot be reused automatically")
		}
		if selected != nil && !strings.EqualFold(selected.Target, *properties.Target) {
			return fmt.Errorf("effective AppInsights connections have conflicting destinations; no connection was changed")
		}
		if selected == nil {
			selected = &MonitoringConnection{Name: name, Target: *properties.Target}
		}
		return nil
	}
	if projectExists {
		pager := factory.NewProjectConnectionsClient().NewListPager(
			project.ResourceGroupName, project.Parent.Name, project.Name, nil)
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
				name := *connection.Name
				properties := connection.Properties.GetConnectionPropertiesV2()
				if err := add(name, properties); err != nil {
					return nil, err
				}
				projectConnections[strings.ToLower(name)] = properties
			}
		}
	}
	if accountExists {
		pager := factory.NewAccountConnectionsClient().NewListPager(
			project.ResourceGroupName, project.Parent.Name, nil)
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
				name := *connection.Name
				properties := connection.Properties.GetConnectionPropertiesV2()
				if properties == nil || properties.Category == nil {
					return nil, fmt.Errorf("account connection properties are unavailable")
				}
				shared := properties.IsSharedToAll != nil && *properties.IsSharedToAll
				if prior := projectConnections[strings.ToLower(name)]; prior != nil && !shared {
					// ARM mirrors project entries at account scope with different IDs.
					sameTarget := properties.Target == nil && prior.Target == nil
					if properties.Target != nil && prior.Target != nil {
						sameTarget = strings.EqualFold(*properties.Target, *prior.Target)
					}
					if !sameTarget || !strings.EqualFold(string(*properties.Category), string(*prior.Category)) {
						return nil, fmt.Errorf("project and account connection identities are ambiguous")
					}
					continue
				}
				if !shared {
					if strings.EqualFold(name, reserved) {
						return nil, fmt.Errorf("the system monitoring name is protected by another account connection")
					}
					if strings.EqualFold(string(*properties.Category), "AppInsights") &&
						(properties.IsSharedToAll == nil || len(properties.SharedUserList) > 0) {
						return nil, fmt.Errorf("restricted account monitoring sharing cannot be resolved safely")
					}
					// An unshared connection belonging to a sibling project is not inherited.
					continue
				}
				if err := add(name, properties); err != nil {
					return nil, err
				}
			}
		}
	}
	if selected == nil && projectExists {
		_, err := factory.NewProjectConnectionsClient().Get(
			ctx, project.ResourceGroupName, project.Parent.Name, project.Name, reserved, nil)
		if err == nil {
			return nil, fmt.Errorf("the system monitoring connection name is already in use")
		}
		if !monitoringNotFound(err) {
			return nil, MonitoringReadError("confirm system monitoring connection name is available", err)
		}
	}
	return selected, nil
}

// MonitoringProperties requires a successfully decoded policy object.
func MonitoringProperties(resource *armresources.GenericResource) (map[string]any, error) {
	properties, ok := resource.Properties.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("monitoring resource policy is unavailable")
	}
	return properties, nil
}

// MonitoringKeyPolicy rejects policy changes needed to enable ApiKey ingestion.
func MonitoringKeyPolicy(properties map[string]any) error {
	if value, present := properties["DisableLocalAuth"]; present {
		disabled, ok := value.(bool)
		if !ok || disabled {
			return fmt.Errorf("automatic ApiKey monitoring cannot change or bypass disabled local authentication")
		}
	}
	if features, present := properties["features"]; present {
		values, ok := features.(map[string]any)
		if !ok {
			return fmt.Errorf("monitoring workspace authentication policy is unavailable")
		}
		if disabled, present := values["disableLocalAuth"]; present && disabled != false {
			return fmt.Errorf("automatic ApiKey monitoring cannot change or bypass disabled workspace local authentication")
		}
	}
	return MonitoringNetworkPolicy(properties)
}

// MonitoringNetworkPolicy rejects unsupported private/restricted ingestion.
func MonitoringNetworkPolicy(properties map[string]any) error {
	for _, name := range []string{"publicNetworkAccessForIngestion", "publicNetworkAccess"} {
		if value, present := properties[name]; present {
			access, ok := value.(string)
			if !ok || !strings.EqualFold(access, "Enabled") {
				return fmt.Errorf("automatic monitoring cannot change or bypass restricted public ingestion")
			}
		}
	}
	if acls, present := properties["networkAcls"]; present {
		values, ok := acls.(map[string]any)
		if !ok {
			return fmt.Errorf("monitoring network policy cannot be resolved")
		}
		if action, present := values["defaultAction"]; present && action != "Allow" {
			return fmt.Errorf("automatic monitoring does not configure private-network ingestion")
		}
	}
	return nil
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

func monitoringNotFound(err error) bool {
	response, ok := errors.AsType[*azcore.ResponseError](err)
	return ok && response.StatusCode == 404
}
