package keyvault

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
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
}

type keyVaultService struct {
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
	coreClientOptions  *azcore.ClientOptions
}

// NewKeyVaultService creates a new KeyVault service
func NewKeyVaultService(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
	coreClientOptions *azcore.ClientOptions,
) KeyVaultService {
	return &keyVaultService{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
		coreClientOptions:  coreClientOptions,
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
