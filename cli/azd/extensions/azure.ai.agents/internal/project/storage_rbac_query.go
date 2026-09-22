// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
)

const (
	storageBlobContributorRole = "ba92f5b4-2d11-453d-a403-e96b0029c9fe"
	storageBlobOwnerRole       = "b7e6dc6d-f1e8-4753-8033-0f276bb0955b"
)

type storagePermission string

const (
	storagePermissionGranted storagePermission = "granted"
	storagePermissionMissing storagePermission = "missing"
	storagePermissionUnknown storagePermission = "unknown"
)

// StorageRBACFinding describes the role assessment for one project storage connection.
type StorageRBACFinding struct {
	ConnectionName string
	StorageScope   string
	Status         string
	Message        string
}

// ProjectStorageRBACResult contains read-only findings for the project identity, not the caller.
type ProjectStorageRBACResult struct {
	PrincipalID string
	Findings    []StorageRBACFinding
}

// QueryProjectStorageRBAC checks project Storage connections without reading secrets or changing Azure resources.
func QueryProjectStorageRBAC(
	ctx context.Context, azdClient *azdext.AzdClient, projectResourceID string,
) (*ProjectStorageRBACResult, error) {
	info, err := storageProjectInfo(projectResourceID)
	if err != nil {
		return nil, err
	}
	tenant, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{SubscriptionId: info.SubscriptionID})
	if err != nil {
		return nil, fmt.Errorf("lookup project tenant: %w", err)
	}
	if tenant == nil || tenant.TenantId == "" {
		return nil, errors.New("project tenant is unavailable")
	}
	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID: tenant.TenantId,
	})
	if err != nil {
		return nil, fmt.Errorf("create storage diagnostic credential: %w", err)
	}
	return queryProjectStorageRBAC(ctx, projectResourceID, credential, azure.NewArmClientOptions())
}

func storageProjectInfo(resourceID string) (*agentIdentityInfo, error) {
	parsed, err := arm.ParseResourceID(resourceID)
	if err != nil || !strings.EqualFold(parsed.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects") ||
		parsed.SubscriptionID == "" || parsed.ResourceGroupName == "" || strings.ContainsAny(resourceID, "?#%\\") {
		return nil, ErrInvalidProjectResourceID
	}
	return &agentIdentityInfo{
		SubscriptionID: parsed.SubscriptionID, ResourceGroup: parsed.ResourceGroupName,
		AccountName: parsed.Parent.Name, ProjectName: parsed.Name,
		ProjectScope: parsed.String(), AccountScope: parsed.Parent.String(),
	}, nil
}

func queryProjectStorageRBAC(
	ctx context.Context, projectResourceID string, credential azcore.TokenCredential, options *arm.ClientOptions,
) (*ProjectStorageRBACResult, error) {
	info, err := storageProjectInfo(projectResourceID)
	if err != nil {
		return nil, err
	}
	factory, err := armcognitiveservices.NewClientFactory(info.SubscriptionID, credential, options)
	if err != nil {
		return nil, err
	}
	projectResponse, err := factory.NewProjectsClient().Get(ctx, info.ResourceGroup, info.AccountName, info.ProjectName, nil)
	if err != nil {
		return nil, fmt.Errorf("read project identity: %w", err)
	}
	result := &ProjectStorageRBACResult{}
	if identity := projectResponse.Identity; identity != nil && identity.PrincipalID != nil {
		result.PrincipalID = *identity.PrincipalID
	}
	connections := map[string]*armcognitiveservices.ConnectionPropertiesV2BasicResource{}
	accountPager := factory.NewAccountConnectionsClient().NewListPager(info.ResourceGroup, info.AccountName, nil)
	for accountPager.More() {
		page, pageErr := accountPager.NextPage(ctx)
		if pageErr != nil {
			return nil, fmt.Errorf("list shared storage connections: %w", pageErr)
		}
		for _, connection := range page.Value {
			if connection == nil || connection.Properties == nil || connection.Name == nil {
				return nil, errors.New("incomplete shared connection metadata")
			}
			properties := connection.Properties.GetConnectionPropertiesV2()
			if properties == nil {
				return nil, errors.New("incomplete shared connection properties")
			}
			shared := properties.IsSharedToAll != nil && *properties.IsSharedToAll
			for _, projectID := range properties.SharedUserList {
				if projectID != nil && strings.EqualFold(*projectID, info.ProjectScope) {
					shared = true
				}
			}
			if shared {
				connections[strings.ToLower(*connection.Name)] = connection
			}
		}
	}
	projectPager := factory.NewProjectConnectionsClient().NewListPager(
		info.ResourceGroup, info.AccountName, info.ProjectName, nil)
	for projectPager.More() {
		page, pageErr := projectPager.NextPage(ctx)
		if pageErr != nil {
			return nil, fmt.Errorf("list project storage connections: %w", pageErr)
		}
		for _, connection := range page.Value {
			if connection == nil || connection.Properties == nil || connection.Name == nil {
				return nil, errors.New("incomplete project connection metadata")
			}
			connections[strings.ToLower(*connection.Name)] = connection
		}
	}
	seen := map[string]StorageRBACFinding{}
	for _, connection := range connections {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		finding, relevant := storageConnectionFinding(connection)
		if !relevant {
			continue
		}
		if finding.Status != "" {
			result.Findings = append(result.Findings, finding)
			continue
		}
		if result.PrincipalID == "" {
			finding.Status, finding.Message = "invalid", "the project managed identity is not configured"
			if projectResponse.Identity != nil && len(projectResponse.Identity.UserAssignedIdentities) > 0 {
				finding.Status, finding.Message = "unknown", "the storage access identity could not be determined"
			}
		} else if principal, parseErr := uuid.Parse(result.PrincipalID); parseErr != nil || principal == uuid.Nil {
			finding.Status, finding.Message = "unknown", "the project managed identity metadata is incomplete"
		} else if cached, exists := seen[strings.ToLower(finding.StorageScope)]; exists {
			finding.Status, finding.Message = cached.Status, cached.Message
		} else {
			finding.Status, finding.Message = queryStorageRoles(
				ctx, credential, options, result.PrincipalID, finding.StorageScope)
			seen[strings.ToLower(finding.StorageScope)] = finding
		}
		result.Findings = append(result.Findings, finding)
	}
	slices.SortFunc(result.Findings, func(left, right StorageRBACFinding) int {
		return strings.Compare(left.ConnectionName, right.ConnectionName)
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func storageConnectionFinding(
	connection *armcognitiveservices.ConnectionPropertiesV2BasicResource,
) (StorageRBACFinding, bool) {
	if connection == nil || connection.Properties == nil || connection.Name == nil {
		return StorageRBACFinding{Status: "unknown", Message: "connection metadata is incomplete"}, true
	}
	properties := connection.Properties.GetConnectionPropertiesV2()
	finding := StorageRBACFinding{ConnectionName: *connection.Name}
	if properties == nil || properties.Category == nil {
		finding.Status, finding.Message = "unknown", "connection category is unavailable"
		return finding, true
	}
	category := string(*properties.Category)
	if !strings.EqualFold(category, "AzureBlob") && !strings.EqualFold(category, "AzureStorageAccount") {
		return finding, false
	}
	if properties.AuthType == nil {
		finding.Status, finding.Message = "unknown", "storage authentication mode is unavailable"
		return finding, true
	}
	switch *properties.AuthType {
	case armcognitiveservices.ConnectionAuthTypeAccountKey, armcognitiveservices.ConnectionAuthTypeAPIKey,
		armcognitiveservices.ConnectionAuthTypeAccessKey, armcognitiveservices.ConnectionAuthTypeSAS,
		armcognitiveservices.ConnectionAuthTypeServicePrincipal:
		finding.Status, finding.Message = "skip", "storage does not use the project managed identity"
		return finding, true
	case armcognitiveservices.ConnectionAuthTypeAAD:
		if properties.UseWorkspaceManagedIdentity != nil && !*properties.UseWorkspaceManagedIdentity {
			finding.Status, finding.Message = "skip", "connection does not use the project managed identity"
			return finding, true
		}
	case armcognitiveservices.ConnectionAuthType("ProjectManagedIdentity"):
	case armcognitiveservices.ConnectionAuthTypeManagedIdentity:
		if properties.UseWorkspaceManagedIdentity == nil || !*properties.UseWorkspaceManagedIdentity {
			finding.Status, finding.Message = "unknown", "the connection's managed identity could not be determined"
			return finding, true
		}
	default:
		finding.Status, finding.Message = "unknown", "storage authentication mode is not supported by this check"
		return finding, true
	}
	for key, value := range properties.Metadata {
		if strings.EqualFold(key, "ResourceId") && value != nil {
			finding.StorageScope = strings.TrimSpace(*value)
		}
	}
	if finding.StorageScope == "" {
		finding.Status, finding.Message = "unknown", "storage resource ID is unavailable in the connection metadata"
		return finding, true
	}
	resource, err := arm.ParseResourceID(finding.StorageScope)
	if err != nil || !strings.EqualFold(resource.ResourceType.String(), "Microsoft.Storage/storageAccounts") ||
		resource.SubscriptionID == "" || resource.ResourceGroupName == "" ||
		strings.ContainsAny(finding.StorageScope, "?#%\\") {
		finding.StorageScope = ""
		finding.Status, finding.Message = "invalid", "connection does not identify a valid Storage account resource"
		return finding, true
	}
	finding.StorageScope = resource.String()
	return finding, true
}

func queryStorageRoles(
	ctx context.Context, credential azcore.TokenCredential, options *arm.ClientOptions, principalID, scope string,
) (string, string) {
	resource, err := arm.ParseResourceID(scope)
	if err != nil {
		return "invalid", "storage resource ID is invalid"
	}
	resources, err := armresources.NewClient(resource.SubscriptionID, credential, options)
	if err != nil {
		return "unknown", "could not initialize the storage resource query"
	}
	if _, err := resources.GetByID(ctx, scope, "2023-05-01", nil); err != nil {
		if response, ok := errors.AsType[*azcore.ResponseError](err); ok && response.StatusCode == http.StatusNotFound {
			return "invalid", "the configured Storage account was not found"
		}
		return "unknown", "could not read the configured Storage account"
	}
	roles, err := armauthorization.NewRoleAssignmentsClient(resource.SubscriptionID, credential, options)
	if err != nil {
		return "unknown", "could not initialize the role assignment query"
	}
	pager := roles.NewListForScopePager(scope, &armauthorization.RoleAssignmentsClientListForScopeOptions{
		Filter: new(fmt.Sprintf("assignedTo('%s')", principalID)),
	})
	var assignments []*armauthorization.RoleAssignment
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return "unknown", "could not read all role assignments; verify role-assignment read access and retry"
		}
		assignments = append(assignments, page.Value...)
	}
	assessment := assessStorageRoles(assignments, principalID, scope)
	switch assessment {
	case storagePermissionGranted:
		return string(assessment), "required Blob data role assignment found"
	case storagePermissionMissing:
		return string(assessment), "required Blob data role assignment is missing"
	default:
		return string(assessment), "custom, conditional, or group-based permissions could not be verified"
	}
}

// assessStorageRoles consumes assignedTo-filtered results, including groups resolved for the project principal.
func assessStorageRoles(
	assignments []*armauthorization.RoleAssignment, principalID, storageScope string,
) storagePermission {
	result := storagePermissionMissing
	managementGroupPrefix := strings.ToLower("/providers/Microsoft.Management/managementGroups/")
	for _, assignment := range assignments {
		if assignment == nil || assignment.Properties == nil {
			result = storagePermissionUnknown
			continue
		}
		properties := assignment.Properties
		if properties.PrincipalID == nil || properties.Scope == nil || properties.RoleDefinitionID == nil {
			result = storagePermissionUnknown
			continue
		}
		principal := *properties.PrincipalID
		scope := strings.ToLower(strings.TrimRight(*properties.Scope, "/"))
		target := strings.ToLower(strings.TrimRight(storageScope, "/"))
		if principal == "" || scope == "" {
			result = storagePermissionUnknown
			continue
		}
		inheritedManagementGroup := strings.HasPrefix(scope, managementGroupPrefix)
		if scope != target && !strings.HasPrefix(target, scope+"/") && !inheritedManagementGroup {
			continue
		}
		roleID := strings.ToLower(*properties.RoleDefinitionID)
		roleID = roleID[strings.LastIndex(roleID, "/")+1:]
		switch roleID {
		case "acdd72a7-3385-48ef-bd42-f606fba81ae7", roleContributor, roleOwner,
			"17d1049b-9a84-46fb-8f53-869881c3d3ab", "2a2b9908-6ea1-4ae2-8e65-a410df84e7d1":
			continue
		}
		groupAssignment := properties.PrincipalType != nil &&
			(*properties.PrincipalType == armauthorization.PrincipalTypeGroup ||
				*properties.PrincipalType == armauthorization.PrincipalTypeForeignGroup)
		if !strings.EqualFold(principal, principalID) && !groupAssignment {
			if properties.PrincipalType == nil {
				result = storagePermissionUnknown
			}
			continue
		}
		switch roleID {
		case storageBlobContributorRole, storageBlobOwnerRole:
			if properties.Condition == nil || strings.TrimSpace(*properties.Condition) == "" {
				return storagePermissionGranted
			}
			result = storagePermissionUnknown
		default:
			result = storagePermissionUnknown
		}
	}
	return result
}
