package azcli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/keyvault/azsecrets"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
)

type AzCliKeyVault struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	Location   string `json:"location"`
	Properties struct {
		EnableSoftDelete      bool `json:"enableSoftDelete"`
		EnablePurgeProtection bool `json:"enablePurgeProtection"`
	} `json:"properties"`
}

type AzCliKeyVaultSecret struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (cli *azCli) GetKeyVault(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	vaultName string,
) (*AzCliKeyVault, error) {
	client, err := cli.createKeyVaultClient(ctx, subscriptionId)
	if err != nil {
		return nil, err
	}

	vault, err := client.Get(ctx, resourceGroupName, vaultName, nil)
	if err != nil {
		return nil, fmt.Errorf("getting keyvault: %w", err)
	}

	return &AzCliKeyVault{
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

func (cli *azCli) GetKeyVaultSecret(ctx context.Context, vaultName string, secretName string) (*AzCliKeyVaultSecret, error) {
	vaultUrl := vaultName
	if !strings.Contains(strings.ToLower(vaultName), "https://") {
		vaultUrl = fmt.Sprintf("https://%s.vault.azure.net", vaultName)
	}

	client, err := cli.createSecretsDataClient(ctx, vaultUrl)
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

	return &AzCliKeyVaultSecret{
		Id:    response.SecretBundle.ID.Version(),
		Name:  response.SecretBundle.ID.Name(),
		Value: *response.SecretBundle.Value,
	}, nil
}

func (cli *azCli) PurgeKeyVault(ctx context.Context, subscriptionId string, vaultName string, location string) error {
	client, err := cli.createKeyVaultClient(ctx, subscriptionId)
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
func (cli *azCli) createKeyVaultClient(ctx context.Context, subscriptionId string) (*armkeyvault.VaultsClient, error) {
	options := cli.createDefaultClientOptionsBuilder(ctx).BuildArmClientOptions()
	client, err := armkeyvault.NewVaultsClient(subscriptionId, cli.credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Resource client: %w", err)
	}

	return client, nil
}

// Creates a KeyVault client for data plan operations
// Data plane client is able to fetch secret values. ARM control plane client never returns secret values.
func (cli *azCli) createSecretsDataClient(ctx context.Context, vaultUrl string) (*azsecrets.Client, error) {
	coreOptions := cli.createDefaultClientOptionsBuilder(ctx).BuildCoreClientOptions()
	options := &azsecrets.ClientOptions{
		ClientOptions:                        *coreOptions,
		DisableChallengeResourceVerification: false,
	}

	return azsecrets.NewClient(vaultUrl, cli.credential, options), nil
}
