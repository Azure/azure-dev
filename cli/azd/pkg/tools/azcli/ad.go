package azcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/google/uuid"
	"github.com/sethvargo/go-retry"
)

// Required model structure for Azure Credentials tools
type AzureCredentials struct {
	ClientId                   string `json:"clientId"`
	ClientSecret               string `json:"clientSecret"`
	SubscriptionId             string `json:"subscriptionId"`
	TenantId                   string `json:"tenantId"`
	ResourceManagerEndpointUrl string `json:"resourceManagerEndpointUrl"`
}

type ErrorWithSuggestion struct {
	Suggestion string
	Err        error
}

func (es *ErrorWithSuggestion) Error() string {
	return es.Err.Error()
}

func (es *ErrorWithSuggestion) Unwrap() error {
	return es.Err
}

// AdService provides actions on top of Azure Active Directory (AD)
type AdService interface {
	GetServicePrincipal(
		ctx context.Context,
		subscriptionId string,
		applicationId string,
	) (*graphsdk.Application, error)
	CreateOrUpdateServicePrincipal(
		ctx context.Context,
		subscriptionId string,
		applicationIdOrName string,
		rolesToAssign []string,
	) (*string, json.RawMessage, error)
}

type adService struct {
	credentialProvider account.SubscriptionCredentialProvider
	httpClient         httputil.HttpClient
	userAgent          string
}

// Creates a new instance of the AdService
func NewAdService(
	credentialProvider account.SubscriptionCredentialProvider,
	httpClient httputil.HttpClient,
) AdService {
	return &adService{
		credentialProvider: credentialProvider,
		httpClient:         httpClient,
		userAgent:          azdinternal.UserAgent(),
	}
}

// GetServicePrincipal gets the service principal for the specified application ID or name
func (ad *adService) GetServicePrincipal(
	ctx context.Context,
	subscriptionId string,
	appIdOrName string,
) (*graphsdk.Application, error) {
	graphClient, err := ad.createGraphClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	var application *graphsdk.Application

	// Attempt to find existing application by ID
	application, _ = ad.getApplicationByAppId(ctx, graphClient, appIdOrName)

	// Fallback to find by name
	if application == nil {
		application, _ = getApplicationByName(ctx, graphClient, appIdOrName)
	}

	if application == nil {
		return nil, fmt.Errorf("could not find application with ID or name '%s'", appIdOrName)
	}

	return application, nil
}

func (ad *adService) CreateOrUpdateServicePrincipal(
	ctx context.Context,
	subscriptionId string,
	applicationIdOrName string,
	roleNames []string,
) (*string, json.RawMessage, error) {
	graphClient, err := ad.createGraphClient(ctx, subscriptionId)
	if err != nil {
		return nil, nil, err
	}

	var application *graphsdk.Application

	// Attempt to find existing application by ID or name
	application, _ = ad.GetServicePrincipal(ctx, subscriptionId, applicationIdOrName)

	// Create new application if not found
	if application == nil {
		// Create application
		application, err = createApplication(ctx, graphClient, applicationIdOrName)
		if err != nil {
			return nil, nil, err
		}
	}

	// Get or create service principal from application
	servicePrincipal, err := ensureServicePrincipal(ctx, graphClient, application)
	if err != nil {
		return nil, nil, err
	}

	// Reset credentials for service principal
	credential, err := resetCredentials(ctx, graphClient, application)
	if err != nil {
		return nil, nil, fmt.Errorf("failed resetting application credentials: %w", err)
	}

	// Apply specified role assignments
	err = ad.ensureRoleAssignments(ctx, subscriptionId, roleNames, servicePrincipal)
	if err != nil {
		return nil, nil, fmt.Errorf("failed applying role assignment: %w", err)
	}

	azureCreds := AzureCredentials{
		ClientId:                   *application.AppId,
		ClientSecret:               *credential.SecretText,
		SubscriptionId:             subscriptionId,
		TenantId:                   *servicePrincipal.AppOwnerOrganizationId,
		ResourceManagerEndpointUrl: "https://management.azure.com/",
	}

	credentialsJson, err := json.Marshal(azureCreds)
	if err != nil {
		return nil, nil, fmt.Errorf("failed marshalling Azure credentials to JSON: %w", err)
	}

	var rawMessage json.RawMessage
	if err := json.Unmarshal(credentialsJson, &rawMessage); err != nil {
		return nil, nil, fmt.Errorf("failed unmarshalling JSON to raw message: %w", err)
	}

	return application.AppId, rawMessage, nil
}

func (ad *adService) getApplicationByAppId(
	ctx context.Context,
	graphClient *graphsdk.GraphClient,
	applicationId string,
) (*graphsdk.Application, error) {
	application, err := graphClient.
		ApplicationById(applicationId).
		GetByAppId(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed retrieving application with id '%s': %w", applicationId, err)
	}

	return application, nil
}

func getApplicationByName(
	ctx context.Context,
	graphClient *graphsdk.GraphClient,
	applicationName string,
) (*graphsdk.Application, error) {
	matchingItems, err := graphClient.
		Applications().
		Filter(fmt.Sprintf("startswith(displayName, '%s')", applicationName)).
		Get(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed retrieving application list: %w", err)
	}

	if (len(matchingItems.Value)) == 0 {
		return nil, fmt.Errorf("no application with name '%s' found", applicationName)
	}

	if len(matchingItems.Value) > 1 {
		return nil, fmt.Errorf("more than 1 application with same name '%s'", applicationName)
	}

	return &matchingItems.Value[0], nil
}

// Gets or creates an application with the specified name
func createApplication(
	ctx context.Context,
	graphClient *graphsdk.GraphClient,
	applicationName string,
) (*graphsdk.Application, error) {
	// Existing application doesn't exist - create a new one
	newApp := &graphsdk.Application{
		DisplayName:         applicationName,
		Description:         convert.RefOf("Autogenerated from Azure Developer CLI"),
		PasswordCredentials: []*graphsdk.ApplicationPasswordCredential{},
	}

	newApp, err := graphClient.Applications().Post(ctx, newApp)
	if err != nil {
		return nil, fmt.Errorf("failed creating application '%s': %w", applicationName, err)
	}

	return newApp, nil
}

// Gets or creates a service principal for the specified application name
func ensureServicePrincipal(
	ctx context.Context,
	client *graphsdk.GraphClient,
	application *graphsdk.Application,
) (*graphsdk.ServicePrincipal, error) {
	matchingItems, err := client.
		ServicePrincipals().
		Filter(fmt.Sprintf("displayName eq '%s'", application.DisplayName)).
		Get(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed retrieving application list: %w", err)
	}

	if len(matchingItems.Value) > 1 {
		return nil, fmt.Errorf("more than 1 application exists with same name '%s'", application.DisplayName)
	}

	if len(matchingItems.Value) == 1 {
		return &matchingItems.Value[0], nil
	}

	// Existing service principal doesn't exist - create a new one.
	newSpn := &graphsdk.ServicePrincipal{
		AppId:       *application.AppId,
		DisplayName: application.DisplayName,
		Description: application.Description,
	}

	newSpn, err = client.ServicePrincipals().Post(ctx, newSpn)
	if err != nil {
		return nil, fmt.Errorf("failed creating service principal '%s': %w", application.DisplayName, err)
	}

	return newSpn, nil
}

// Removes any existing password credentials from the application
// and creates a new password credential
func resetCredentials(
	ctx context.Context,
	client *graphsdk.GraphClient,
	application *graphsdk.Application,
) (*graphsdk.ApplicationPasswordCredential, error) {
	for _, credential := range application.PasswordCredentials {
		err := client.
			ApplicationById(*application.Id).
			RemovePassword(ctx, *credential.KeyId)

		if err != nil {
			return nil, fmt.Errorf("failed removing credentials for KeyId '%s' : %w", *credential.KeyId, err)
		}
	}

	credential, err := client.
		ApplicationById(*application.Id).
		AddPassword(ctx)

	if err != nil {
		return nil, fmt.Errorf(
			"failed adding new password credential for application '%s' : %w",
			application.DisplayName,
			err,
		)
	}

	return credential, nil
}

// Applies the Azure selected RBAC role assignments to the specified service principal
func (ad *adService) ensureRoleAssignments(
	ctx context.Context,
	subscriptionId string,
	roleNames []string,
	servicePrincipal *graphsdk.ServicePrincipal,
) error {
	for _, roleName := range roleNames {
		err := ad.ensureRoleAssignment(ctx, subscriptionId, roleName, servicePrincipal)
		if err != nil {
			return err
		}
	}

	return nil
}

// Applies the Azure selected RBAC role assignments to the specified service principal
func (ad *adService) ensureRoleAssignment(
	ctx context.Context,
	subscriptionId string,
	roleName string,
	servicePrincipal *graphsdk.ServicePrincipal,
) error {
	// Find the specified role in the subscription scope
	scope := azure.SubscriptionRID(subscriptionId)
	roleDefinition, err := ad.getRoleDefinition(ctx, subscriptionId, scope, roleName)
	if err != nil {
		return err
	}

	// Create the new role assignment
	err = ad.applyRoleAssignmentWithRetry(ctx, subscriptionId, roleDefinition, servicePrincipal)
	if err != nil {
		return err
	}

	return nil
}

// Applies the role assignment to the specified service principal
// This operation will retry up to 10 times to ensure the new service principal is available in Azure AD
func (ad *adService) applyRoleAssignmentWithRetry(
	ctx context.Context,
	subscriptionId string,
	roleDefinition *armauthorization.RoleDefinition,
	servicePrincipal *graphsdk.ServicePrincipal,
) error {
	roleAssignmentsClient, err := ad.createRoleAssignmentsClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	scope := azure.SubscriptionRID(subscriptionId)
	roleAssignmentId := uuid.New().String()

	// There is a lag in the application/service principal becoming available in Azure AD
	// This can cause the role assignment operation to fail
	return retry.Do(ctx, retry.WithMaxRetries(10, retry.NewConstant(time.Second*5)), func(ctx context.Context) error {
		_, err = roleAssignmentsClient.Create(ctx, scope, roleAssignmentId, armauthorization.RoleAssignmentCreateParameters{
			Properties: &armauthorization.RoleAssignmentProperties{
				PrincipalID:      servicePrincipal.Id,
				RoleDefinitionID: roleDefinition.ID,
			},
		}, nil)

		if err != nil {
			var responseError *azcore.ResponseError
			// If the response is a 409 conflict then the role has already been assigned.
			if errors.As(err, &responseError) && responseError.StatusCode == http.StatusConflict {
				return nil
			}

			// If the response is a 403 then the required role is missing.
			if errors.As(err, &responseError) && responseError.StatusCode == http.StatusForbidden {
				return &ErrorWithSuggestion{
					Suggestion: fmt.Sprintf("\nSuggested Action: Ensure you have either the `User Access Administrator`, " +
						"Owner` or custom azure roles assigned to your subscription to perform action " +
						"'Microsoft.Authorization/roleAssignments/write', in order to manage role assignments\n"),
					Err: err,
				}
			}

			return retry.RetryableError(
				fmt.Errorf(
					"failed assigning role assignment '%s' to service principal '%s' : %w",
					*roleDefinition.Name,
					servicePrincipal.DisplayName,
					err,
				),
			)
		}

		return nil
	})
}

// Find the Azure role definition for the specified scope and role name
func (ad *adService) getRoleDefinition(
	ctx context.Context,
	subscriptionId string,
	scope string,
	roleName string,
) (*armauthorization.RoleDefinition, error) {
	roleDefinitionsClient, err := ad.createRoleDefinitionsClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	pager := roleDefinitionsClient.NewListPager(scope, &armauthorization.RoleDefinitionsClientListOptions{
		Filter: convert.RefOf(fmt.Sprintf("roleName eq '%s'", roleName)),
	})

	roleDefinitions := []*armauthorization.RoleDefinition{}

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed getting next page of role definitions: %w", err)
		}

		roleDefinitions = append(roleDefinitions, page.RoleDefinitionListResult.Value...)
	}

	if len(roleDefinitions) == 0 {
		return nil, fmt.Errorf("role definition with scope: '%s' and name: '%s' was not found", scope, roleName)
	}

	return roleDefinitions[0], nil
}

// Creates a graph users client using credentials from the Go context.
func (ad *adService) createGraphClient(
	ctx context.Context,
	subscriptionId string,
) (*graphsdk.GraphClient, error) {
	credential, err := ad.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := clientOptionsBuilder(ctx, ad.httpClient, ad.userAgent).BuildCoreClientOptions()
	client, err := graphsdk.NewGraphClient(credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Graph Users client: %w", err)
	}

	return client, nil
}

// Creates a graph users client using credentials from the Go context.
func (ad *adService) createRoleDefinitionsClient(
	ctx context.Context,
	subscriptionId string,
) (*armauthorization.RoleDefinitionsClient, error) {
	credential, err := ad.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := clientOptionsBuilder(ctx, ad.httpClient, ad.userAgent).BuildArmClientOptions()
	client, err := armauthorization.NewRoleDefinitionsClient(credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating ARM Role Definitions client: %w", err)
	}

	return client, nil
}

// Creates a graph users client using credentials from the Go context.
func (ad *adService) createRoleAssignmentsClient(
	ctx context.Context,
	subscriptionId string,
) (*armauthorization.RoleAssignmentsClient, error) {
	credential, err := ad.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := clientOptionsBuilder(ctx, ad.httpClient, ad.userAgent).BuildArmClientOptions()
	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionId, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating ARM Role Assignments client: %w", err)
	}

	return client, nil
}
