package keyvault

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azadmin/rbac"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

var ErrAzCliSecretNotFound = errors.New("secret not found")

type KeyVault struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	Location   string `json:"location"`
	Properties struct {
		EnableSoftDelete      bool `json:"enableSoftDelete"`
		EnablePurgeProtection bool `json:"enablePurgeProtection"`
	} `json:"properties"`
}

type Secret struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type KeyVaultService interface {
	GetKeyVault(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		vaultName string,
	) (*KeyVault, error)
	GetKeyVaultSecret(
		ctx context.Context,
		subscriptionId string,
		vaultName string,
		secretName string,
	) (*Secret, error)
	PurgeKeyVault(ctx context.Context, subscriptionId string, vaultName string, location string) error
	ListSubscriptionVaults(ctx context.Context, subscriptionId string) ([]Vault, error)
	CreateVault(
		ctx context.Context,
		tenantId string,
		subscriptionId string,
		resourceGroupName string,
		location string,
		vaultName string,
	) (Vault, error)
}

type keyVaultService struct {
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
	coreClientOptions  *azcore.ClientOptions
	cloud              *cloud.Cloud
}

// NewKeyVaultService creates a new KeyVault service
func NewKeyVaultService(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
	coreClientOptions *azcore.ClientOptions,
	cloud *cloud.Cloud,
) KeyVaultService {
	return &keyVaultService{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
		coreClientOptions:  coreClientOptions,
		cloud:              cloud,
	}
}

func (kvs *keyVaultService) GetKeyVault(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	vaultName string,
) (*KeyVault, error) {
	client, err := kvs.createKeyVaultClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	vault, err := client.Get(ctx, resourceGroupName, vaultName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting key vault: %w", err)
	}

	return &KeyVault{
		Id:       *vault.ID,
		Name:     *vault.Name,
		Location: *vault.Location,
		Properties: struct {
			EnableSoftDelete      bool "json:\"enableSoftDelete\""
			EnablePurgeProtection bool "json:\"enablePurgeProtection\""
		}{
			EnableSoftDelete:      convert.ToValueWithDefault(vault.Properties.EnableSoftDelete, false),
			EnablePurgeProtection: convert.ToValueWithDefault(vault.Properties.EnablePurgeProtection, false),
		},
	}, nil
}

func (kvs *keyVaultService) GetKeyVaultSecret(
	ctx context.Context,
	subscriptionId string,
	vaultName string,
	secretName string,
) (*Secret, error) {
	vaultUrl := vaultName
	if !strings.Contains(strings.ToLower(vaultName), "https://") {
		vaultUrl = fmt.Sprintf("https://%s.vault.azure.net", vaultName)
	}

	client, err := kvs.createSecretsDataClient(ctx, subscriptionId, vaultUrl)
	if err != nil {
		return nil, nil
	}

	response, err := client.GetSecret(ctx, secretName, "", nil)
	if err != nil {
		var httpErr *azcore.ResponseError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			return nil, ErrAzCliSecretNotFound
		}
		return nil, fmt.Errorf("getting key vault secret: %w", err)
	}

	return &Secret{
		Id:    response.SecretBundle.ID.Version(),
		Name:  response.SecretBundle.ID.Name(),
		Value: *response.SecretBundle.Value,
	}, nil
}

func (kvs *keyVaultService) PurgeKeyVault(
	ctx context.Context, subscriptionId string, vaultName string, location string) error {
	client, err := kvs.createKeyVaultClient(ctx, subscriptionId)
	if err != nil {
		return err
	}

	poller, err := client.BeginPurgeDeleted(ctx, vaultName, location, nil)
	if err != nil {
		var httpErr *azcore.ResponseError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusNotFound {
			// no need to purge if the vault is already deleted (not found)
			log.Printf("key vault '%s' was not found. No need to purge.", vaultName)
			return nil
		}
		return fmt.Errorf("starting purging key vault: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("purging key vault: %w", err)
	}

	return nil
}

// Creates a KeyVault client for ARM control plane operations
func (kvs *keyVaultService) createKeyVaultClient(
	ctx context.Context, subscriptionId string) (*armkeyvault.VaultsClient, error) {
	credential, err := kvs.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	client, err := armkeyvault.NewVaultsClient(subscriptionId, credential, kvs.armClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return client, nil
}

// Creates a KeyVault client for data plan operations
// Data plane client is able to fetch secret values. ARM control plane client never returns secret values.
func (kvs *keyVaultService) createSecretsDataClient(
	ctx context.Context,
	subscriptionId string,
	vaultUrl string,
) (*azsecrets.Client, error) {
	credential, err := kvs.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := &azsecrets.ClientOptions{
		ClientOptions:                        *kvs.coreClientOptions,
		DisableChallengeResourceVerification: false,
	}

	return azsecrets.NewClient(vaultUrl, credential, options)
}

func (kvs *keyVaultService) createRbacClient(
	ctx context.Context,
	subscriptionId string,
	vaultUrl string,
) (*rbac.Client, error) {
	credential, err := kvs.credentialProvider.CredentialForSubscription(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	options := &rbac.ClientOptions{
		ClientOptions:                        *kvs.coreClientOptions,
		DisableChallengeResourceVerification: false,
	}

	return rbac.NewClient(vaultUrl, credential, options)
}

type Vault struct {
	Id   string
	Name string
}

func (kvs *keyVaultService) ListSubscriptionVaults(
	ctx context.Context,
	subscriptionId string,
) ([]Vault, error) {
	client, err := kvs.createKeyVaultClient(ctx, subscriptionId)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}
	result := []Vault{}
	pager := client.NewListPager(nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing vaults: %w", err)
		}
		for _, vault := range page.Value {
			result = append(result, Vault{
				Id:   *vault.ID,
				Name: *vault.Name,
			})
		}
	}
	return result, nil
}

func (kvs *keyVaultService) CreateVault(
	ctx context.Context,
	tenantId string,
	subscriptionId string,
	resourceGroupName string,
	location string,
	vaultName string,
) (Vault, error) {
	client, err := kvs.createKeyVaultClient(ctx, subscriptionId)
	if err != nil {
		return Vault{}, fmt.Errorf("creating Resource client: %w", err)
	}
	accountPoller, err := client.BeginCreateOrUpdate(
		ctx,
		resourceGroupName,
		vaultName,
		armkeyvault.VaultCreateOrUpdateParameters{
			Location: to.Ptr(location),
			Properties: &armkeyvault.VaultProperties{
				SKU: &armkeyvault.SKU{
					Family: to.Ptr(armkeyvault.SKUFamilyA),
					Name:   to.Ptr(armkeyvault.SKUNameStandard),
				},
				TenantID:                to.Ptr(tenantId),
				EnableRbacAuthorization: to.Ptr(true),
			},
		},
		nil)
	if err != nil {
		return Vault{}, fmt.Errorf("creating Key Vault: %w", err)
	}
	response, err := accountPoller.PollUntilDone(ctx, nil)
	if err != nil {
		return Vault{}, fmt.Errorf("creating Key Vault: %w", err)
	}

	return Vault{
		Id:   *response.Vault.ID,
		Name: *response.Vault.Name,
	}, nil
}

// Built-in roles for Key Vault RBAC
// https://learn.microsoft.com/azure/role-based-access-control/built-in-roles
const (
	KeyVaultAdministrator string = "/providers/Microsoft.Authorization/roleDefinitions/00482a5a-887f-4fb3-b363-3b7fe8e74483"
)

// func (kvs *keyVaultService) CreateRbac(
// 	ctx context.Context,
// 	subscriptionId string,
// 	kvAccountName string,
// 	principalId string,
// 	roleId RbacId,
// ) error {
// 	serviceUrl := fmt.Sprintf("https://%s.%s", kvAccountName, kvs.cloud.KeyVaultEndpointSuffix)
// 	client, err := kvs.createRbacClient(ctx, subscriptionId, serviceUrl)
// 	if err != nil {
// 		return fmt.Errorf("creating RBAC client: %w", err)
// 	}
// 	scope := rbac.RoleScopeKeys
// 	name := uuid.New().String()
// 	_, err = client.CreateRoleAssignment(
// 		ctx,
// 		scope,
// 		name,
// 		rbac.RoleAssignmentCreateParameters{
// 			Properties: &rbac.RoleAssignmentProperties{
// 				PrincipalID:      to.Ptr(principalId),
// 				RoleDefinitionID: to.Ptr(string(roleId)),
// 			},
// 		},
// 		nil)
// 	if err != nil {
// 		return fmt.Errorf("creating RBAC: %w", err)
// 	}
// 	return nil
// }
