package entraid

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/google/uuid"
	"github.com/sethvargo/go-retry"
)

const (
	federatedIdentityIssuer   = "https://token.actions.githubusercontent.com"
	federatedIdentityAudience = "api://AzureADTokenExchange"
)

// Required model structure for Azure Credentials tools
type AzureCredentials struct {
	ClientId       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	SubscriptionId string `json:"subscriptionId"`
	TenantId       string `json:"tenantId"`
}

// EntraIdService provides actions on top of Azure Active Directory (AD)
type EntraIdService interface {
	GetServicePrincipal(
		ctx context.Context,
		subscriptionId string,
		appIdOrName string,
	) (*graphsdk.ServicePrincipal, error)
	CreateOrUpdateServicePrincipal(
		ctx context.Context,
		subscriptionId string,
		appIdOrName string,
		options CreateOrUpdateServicePrincipalOptions,
	) (*graphsdk.ServicePrincipal, error)
	ResetPasswordCredentials(
		ctx context.Context,
		subscriptionId string,
		appId string,
	) (*AzureCredentials, error)
	ApplyFederatedCredentials(
		ctx context.Context,
		subscriptionId string,
		clientId string,
		federatedCredentials []*graphsdk.FederatedIdentityCredential,
	) ([]*graphsdk.FederatedIdentityCredential, error)
}

type entraIdService struct {
	credentialProvider account.SubscriptionCredentialProvider
	clientCache        map[string]*graphsdk.GraphClient
	armClientOptions   *arm.ClientOptions
	coreClientOptions  *azcore.ClientOptions
}

// Creates a new instance of the EntraIdService
func NewEntraIdService(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
	coreClientOptions *azcore.ClientOptions,
) EntraIdService {
	return &entraIdService{
		credentialProvider: credentialProvider,
		clientCache:        map[string]*graphsdk.GraphClient{},
		armClientOptions:   armClientOptions,
		coreClientOptions:  coreClientOptions,
	}
}

// GetServicePrincipal gets the service principal for the specified application ID or name
func (ad *entraIdService) GetServicePrincipal(
	ctx context.Context,
	subscriptionId string,
	appIdOrName string,
) (*graphsdk.ServicePrincipal, error) {
	application, err := ad.getApplicationByNameOrId(ctx, subscriptionId, appIdOrName)
	if err != nil {
		return nil, err
	}

	return ad.getServicePrincipal(ctx, subscriptionId, application)
}

type CreateOrUpdateServicePrincipalOptions struct {
	RolesToAssign              []string
	Description                *string
	ServiceManagementReference *string
}

func (ad *entraIdService) CreateOrUpdateServicePrincipal(
	ctx context.Context,
	subscriptionId string,
	appIdOrName string,
	options CreateOrUpdateServicePrincipalOptions,
) (*graphsdk.ServicePrincipal, error) {
	var application *graphsdk.Application
	var err error

	// Attempt to find existing application by ID or name
	application, _ = ad.getApplicationByNameOrId(ctx, subscriptionId, appIdOrName)

	// Create new application if not found
	if application == nil {
		// Create application
		application, err = ad.createApplication(ctx, subscriptionId, appIdOrName, options)
		if err != nil {
			return nil, err
		}
	}

	// Get or create service principal from application
	servicePrincipal, err := ad.ensureServicePrincipal(ctx, subscriptionId, application)
	if err != nil {
		return nil, err
	}

	// Apply specified role assignments
	err = ad.ensureRoleAssignments(ctx, subscriptionId, options.RolesToAssign, servicePrincipal)
	if err != nil {
		return nil, fmt.Errorf("failed applying role assignment: %w", err)
	}

	return servicePrincipal, nil
}

// Removes any existing password credentials from the application
// and creates a new password credential
func (ad *entraIdService) ResetPasswordCredentials(
	ctx context.Context,
	subscriptionId string,
	appId string,
) (*AzureCredentials, error) {
	graphClient, err := ad.getOrCreateGraphClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	application, err := ad.getApplicationByAppId(ctx, subscriptionId, appId)
	if err != nil {
		return nil, fmt.Errorf("failed finding matching application: %w", err)
	}

	servicePrincipal, err := ad.getServicePrincipal(ctx, subscriptionId, application)
	if err != nil {
		return nil, fmt.Errorf("failed finding matching service principal: %w", err)
	}

	for _, credential := range application.PasswordCredentials {
		err := graphClient.
			ApplicationById(*application.Id).
			RemovePassword(ctx, *credential.KeyId)

		if err != nil {
			return nil, fmt.Errorf("failed removing credentials for KeyId '%s' : %w", *credential.KeyId, err)
		}
	}

	credential, err := graphClient.
		ApplicationById(*application.Id).
		AddPassword(ctx)

	if err != nil {
		return nil, fmt.Errorf(
			"failed adding new password credential for application '%s' : %w",
			application.DisplayName,
			err,
		)
	}

	return &AzureCredentials{
		ClientId:       *application.AppId,
		ClientSecret:   *credential.SecretText,
		SubscriptionId: subscriptionId,
		TenantId:       *servicePrincipal.AppOwnerOrganizationId,
	}, nil
}

func (ad *entraIdService) ApplyFederatedCredentials(
	ctx context.Context,
	subscriptionId string,
	clientId string,
	federatedCredentials []*graphsdk.FederatedIdentityCredential,
) ([]*graphsdk.FederatedIdentityCredential, error) {
	graphClient, err := ad.getOrCreateGraphClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	application, err := ad.getApplicationByAppId(ctx, subscriptionId, clientId)
	if err != nil {
		return nil, fmt.Errorf("failed finding matching application: %w", err)
	}

	existingCredsResponse, err := graphClient.
		ApplicationById(*application.Id).
		FederatedIdentityCredentials().
		Get(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed retrieving federated credentials: %w", err)
	}

	existingCredentials := existingCredsResponse.Value
	createdCredentials := []*graphsdk.FederatedIdentityCredential{}

	// Ensure the credential exists otherwise create a new one.
	for i := range federatedCredentials {
		credential, err := ad.ensureFederatedCredential(
			ctx,
			subscriptionId,
			application,
			existingCredentials,
			federatedCredentials[i],
		)
		if err != nil {
			return nil, err
		}

		if credential != nil {
			createdCredentials = append(createdCredentials, credential)
		}
	}

	return createdCredentials, nil
}

func (ad *entraIdService) getApplicationByNameOrId(
	ctx context.Context,
	subscriptionId string,
	appIdOrName string,
) (*graphsdk.Application, error) {
	// Attempt to find existing application by ID
	application, _ := ad.getApplicationByAppId(ctx, subscriptionId, appIdOrName)

	// Fallback to find by name
	if application == nil {
		application, _ = ad.getApplicationByName(ctx, subscriptionId, appIdOrName)
	}

	if application == nil {
		return nil, fmt.Errorf("could not find application with ID or name '%s'", appIdOrName)
	}

	return application, nil
}

func (ad *entraIdService) getApplicationByAppId(
	ctx context.Context,
	subscriptionId string,
	appId string,
) (*graphsdk.Application, error) {
	graphClient, err := ad.getOrCreateGraphClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	application, err := graphClient.
		ApplicationById(appId).
		GetByAppId(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed retrieving application with id '%s': %w", appId, err)
	}

	return application, nil
}

func (ad *entraIdService) getApplicationByName(
	ctx context.Context,
	subscriptionId string,
	applicationName string,
) (*graphsdk.Application, error) {
	graphClient, err := ad.getOrCreateGraphClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

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
func (ad *entraIdService) createApplication(
	ctx context.Context,
	subscriptionId string,
	applicationName string,
	options CreateOrUpdateServicePrincipalOptions,
) (*graphsdk.Application, error) {
	graphClient, err := ad.getOrCreateGraphClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	// Existing application doesn't exist - create a new one
	newApp := &graphsdk.Application{
		DisplayName:                applicationName,
		Description:                options.Description,
		PasswordCredentials:        []*graphsdk.ApplicationPasswordCredential{},
		ServiceManagementReference: options.ServiceManagementReference,
	}

	newApp, err = graphClient.Applications().Post(ctx, newApp)
	if err != nil {
		return nil, fmt.Errorf("failed creating application '%s': %w", applicationName, err)
	}

	return newApp, nil
}

func (ad *entraIdService) getServicePrincipal(
	ctx context.Context,
	subscriptionId string,
	application *graphsdk.Application,
) (*graphsdk.ServicePrincipal, error) {
	graphClient, err := ad.getOrCreateGraphClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	matchingItems, err := graphClient.
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

	return nil, fmt.Errorf("no service principal found for application '%s'", application.DisplayName)
}

// Gets or creates a service principal for the specified application name
func (ad *entraIdService) ensureServicePrincipal(
	ctx context.Context,
	subscriptionId string,
	application *graphsdk.Application,
) (*graphsdk.ServicePrincipal, error) {
	graphClient, err := ad.getOrCreateGraphClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	servicePrincipal, err := ad.getServicePrincipal(ctx, subscriptionId, application)
	if err == nil && servicePrincipal != nil {
		return servicePrincipal, nil
	}

	// Existing service principal doesn't exist - create a new one.
	newSpn := &graphsdk.ServicePrincipal{
		AppId:       *application.AppId,
		DisplayName: application.DisplayName,
		Description: application.Description,
	}

	newSpn, err = graphClient.ServicePrincipals().Post(ctx, newSpn)
	if err != nil {
		return nil, fmt.Errorf("failed creating service principal '%s': %w", application.DisplayName, err)
	}

	return newSpn, nil
}

// Ensures that the federated credential exists on the application otherwise create a new one
func (ad *entraIdService) ensureFederatedCredential(
	ctx context.Context,
	subscriptionId string,
	application *graphsdk.Application,
	existingCredentials []graphsdk.FederatedIdentityCredential,
	repoCredential *graphsdk.FederatedIdentityCredential,
) (*graphsdk.FederatedIdentityCredential, error) {
	graphClient, err := ad.getOrCreateGraphClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	// If a federated credential already exists for the same subject then nothing to do.
	for _, existing := range existingCredentials {
		if existing.Subject == repoCredential.Subject {
			log.Printf(
				"federated credential with subject '%s' already exists on application '%s'",
				repoCredential.Subject,
				*application.Id,
			)
			return nil, nil
		}
	}

	// Otherwise create the new federated credential
	credential, err := graphClient.
		ApplicationById(*application.Id).
		FederatedIdentityCredentials().
		Post(ctx, repoCredential)

	if err != nil {
		return nil, fmt.Errorf("failed creating federated credential: %w", err)
	}

	return credential, nil
}

// Applies the Azure selected RBAC role assignments to the specified service principal
func (ad *entraIdService) ensureRoleAssignments(
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
func (ad *entraIdService) ensureRoleAssignment(
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
func (ad *entraIdService) applyRoleAssignmentWithRetry(
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
				return &internal.ErrorWithSuggestion{
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
func (ad *entraIdService) getRoleDefinition(
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
func (ad *entraIdService) createRoleDefinitionsClient(
	ctx context.Context,
	subscriptionId string,
) (*armauthorization.RoleDefinitionsClient, error) {
	credential, err := ad.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armauthorization.NewRoleDefinitionsClient(credential, ad.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating ARM Role Definitions client: %w", err)
	}

	return client, nil
}

// Creates a graph users client using credentials from the Go context.
func (ad *entraIdService) createRoleAssignmentsClient(
	ctx context.Context,
	subscriptionId string,
) (*armauthorization.RoleAssignmentsClient, error) {
	credential, err := ad.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armauthorization.NewRoleAssignmentsClient(subscriptionId, credential, ad.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating ARM Role Assignments client: %w", err)
	}

	return client, nil
}

// Creates a graph users client using credentials from the Go context.
func (ad *entraIdService) getOrCreateGraphClient(
	ctx context.Context,
	subscriptionId string,
) (*graphsdk.GraphClient, error) {
	if client, ok := ad.clientCache[subscriptionId]; ok {
		return client, nil
	}

	credential, err := ad.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := graphsdk.NewGraphClient(credential, ad.coreClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating Graph Users client: %w", err)
	}

	ad.clientCache[subscriptionId] = client

	return client, nil
}
