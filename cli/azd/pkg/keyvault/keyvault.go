// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package keyvault

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
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
	ListKeyVaultSecrets(
		ctx context.Context,
		subscriptionId string,
		vaultName string,
	) ([]string, error)
	CreateKeyVaultSecret(
		ctx context.Context,
		subscriptionId string,
		vaultName string,
		secretName string,
		secretValue string,
	) error
	SecretFromAkvs(ctx context.Context, akvs string) (string, error)
	// SecretFromKeyVaultReference resolves a secret reference in either the
	// akvs:// or @Microsoft.KeyVault(SecretUri=...) format. The subscriptionId
	// is required for credential scoping; for @Microsoft.KeyVault references
	// (which lack a subscription), the caller should provide the environment's
	// default subscription.
	SecretFromKeyVaultReference(ctx context.Context, ref string, defaultSubscriptionId string) (string, error)
}

type keyVaultService struct {
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
	coreClientOptions  *azcore.ClientOptions
	cloud              *cloud.Cloud
}

// CreateKeyVaultSecret implements KeyVaultService.
func (kvs *keyVaultService) CreateKeyVaultSecret(
	ctx context.Context, subscriptionId string, vaultName string, secretName string, secretValue string) error {
	client, err := kvs.createSecretsDataClient(ctx, subscriptionId, vaultName)
	if err != nil {
		return err
	}
	_, err = client.SetSecret(ctx, secretName, azsecrets.SetSecretParameters{
		Value: new(secretValue),
	}, nil)
	return err
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
	client, err := kvs.createSecretsDataClient(ctx, subscriptionId, vaultName)
	if err != nil {
		return nil, err
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
		Id:    response.ID.Version(),
		Name:  response.ID.Name(),
		Value: *response.Value,
	}, nil
}

func (kvs *keyVaultService) ListKeyVaultSecrets(
	ctx context.Context,
	subscriptionId string,
	vaultName string,
) ([]string, error) {
	client, err := kvs.createSecretsDataClient(ctx, subscriptionId, vaultName)
	if err != nil {
		return nil, nil
	}

	secretsPager := client.NewListSecretPropertiesPager(nil)
	result := []string{}
	for secretsPager.More() {
		secretsPage, err := secretsPager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing key vault secrets: %w", err)
		}

		for _, secret := range secretsPage.Value {
			result = append(result, secret.ID.Name())
		}
	}
	return result, nil
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
	vaultName string,
) (*azsecrets.Client, error) {
	vaultUrl := vaultName
	if !strings.Contains(strings.ToLower(vaultName), "https://") {
		vaultUrl = fmt.Sprintf("https://%s.%s", vaultName, kvs.cloud.KeyVaultEndpointSuffix)
	}
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
			Location: new(location),
			Properties: &armkeyvault.VaultProperties{
				SKU: &armkeyvault.SKU{
					Family: to.Ptr(armkeyvault.SKUFamilyA),
					Name:   to.Ptr(armkeyvault.SKUNameStandard),
				},
				TenantID:                new(tenantId),
				EnableRbacAuthorization: new(true),
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
	vaultSchemaAkvs             string = "akvs://"
	resourceIdPathPrefix        string = "/providers/Microsoft.Authorization/roleDefinitions/"
	RoleIdKeyVaultAdministrator string = resourceIdPathPrefix + "00482a5a-887f-4fb3-b363-3b7fe8e74483"
	RoleIdKeyVaultSecretsUser   string = resourceIdPathPrefix + "4633458b-17de-408a-b874-0445c86b69e6"
)

func IsAzureKeyVaultSecret(id string) bool {
	return strings.HasPrefix(id, vaultSchemaAkvs)
}

func IsValidSecretName(kvSecretName string) bool {
	return len(kvSecretName) >= 1 && len(kvSecretName) <= 127 && strings.IndexFunc(kvSecretName, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-'
	}) == -1
}

func NewAzureKeyVaultSecret(subId, vaultId, secretName string) string {
	return vaultSchemaAkvs + subId + "/" + vaultId + "/" + secretName
}

func (kvs *keyVaultService) SecretFromAkvs(ctx context.Context, akvs string) (string, error) {
	parseAkvs, err := ParseAzureKeyVaultSecret(akvs)
	if err != nil {
		return "", err
	}

	// subscriptionId is required by the Key Vault service to figure the TenantId for the
	// tokenCredential. The assumption here is that the user has access to the Tenant
	// used to deploy the app and to whatever Tenant the Key Vault is in. And the tokenCredential
	// can use any of the Tenant ids.
	secretValue, err := kvs.GetKeyVaultSecret(
		ctx, parseAkvs.SubscriptionId, parseAkvs.VaultName, parseAkvs.SecretName)
	if err != nil {
		return "", fmt.Errorf("fetching secret value from key vault: %w", err)
	}
	return secretValue.Value, nil
}

func (kvs *keyVaultService) SecretFromKeyVaultReference(
	ctx context.Context, ref string, defaultSubscriptionId string,
) (string, error) {
	// Try akvs:// first (includes its own subscription ID)
	if IsAzureKeyVaultSecret(ref) {
		return kvs.SecretFromAkvs(ctx, ref)
	}

	// Try @Microsoft.KeyVault(SecretUri=...)
	if IsKeyVaultAppReference(ref) {
		parsed, err := ParseKeyVaultAppReference(ref)
		if err != nil {
			return "", err
		}

		// Use the vault URL directly. The subscription ID is only needed
		// for credential scoping (tenant lookup), so we use the default.
		client, err := kvs.createSecretsDataClient(ctx, defaultSubscriptionId, parsed.VaultURL)
		if err != nil {
			return "", fmt.Errorf("creating Key Vault client for %s: %w", parsed.VaultURL, err)
		}

		resp, err := client.GetSecret(ctx, parsed.SecretName, parsed.SecretVersion, nil)
		if err != nil {
			return "", fmt.Errorf("fetching secret %q from vault %q: %w",
				parsed.SecretName, parsed.VaultName, err)
		}

		if resp.Value == nil {
			return "", fmt.Errorf("secret %q in vault %q has a nil value",
				parsed.SecretName, parsed.VaultName)
		}

		return *resp.Value, nil
	}

	return "", fmt.Errorf("unrecognized Key Vault reference format: %s", ref)
}

// AzureKeyVaultSecret represents a secret stored in an Azure Key Vault.
// It contains the necessary information to identify and access the secret.
//
// Fields:
// - SubscriptionId: The ID of the Azure subscription that contains the Key Vault.
// - VaultName: The name of the Key Vault where the secret is stored.
// - SecretName: The name of the secret within the Key Vault.
type AzureKeyVaultSecret struct {
	SubscriptionId string
	VaultName      string
	SecretName     string
}

// ParseAzureKeyVaultSecret parses a string representing an Azure Key Vault Secret reference
// and returns an AzureKeyVaultSecret struct if the reference is valid.
//
// The expected format for the Azure Key Vault Secret reference is:
// "akvs://<subscription-id>/<vault-name>/<secret-name>"
//
// Parameters:
//   - akvs: A string representing the Azure Key Vault Secret reference.
//
// Returns:
//   - AzureKeyVaultSecret: A struct containing the subscription ID, vault name, and secret name.
//   - error: An error if the Azure Key Vault Secret reference is invalid.
func ParseAzureKeyVaultSecret(akvs string) (AzureKeyVaultSecret, error) {
	if !IsAzureKeyVaultSecret(akvs) {
		return AzureKeyVaultSecret{}, fmt.Errorf("invalid Azure Key Vault Secret reference: %s", akvs)
	}

	noSchema := strings.TrimPrefix(akvs, vaultSchemaAkvs)
	vaultParts := strings.Split(noSchema, "/")
	if len(vaultParts) != 3 {
		return AzureKeyVaultSecret{}, fmt.Errorf(
			"invalid Azure Key Vault Secret reference: %s. Expected format: %s",
			akvs,
			vaultSchemaAkvs+"<subscription-id>/<vault-name>/<secret-name>",
		)
	}
	return AzureKeyVaultSecret{
		SubscriptionId: vaultParts[0],
		VaultName:      vaultParts[1],
		SecretName:     vaultParts[2],
	}, nil
}

const keyVaultAppRefPrefix = "@Microsoft.KeyVault("

// IsKeyVaultAppReference reports whether s uses the @Microsoft.KeyVault(SecretUri=...) format
// used by Azure App Service and App Configuration for Key Vault references.
func IsKeyVaultAppReference(s string) bool {
	return strings.HasPrefix(s, keyVaultAppRefPrefix) && strings.HasSuffix(s, ")")
}

// IsSecretReference reports whether s is a Key Vault secret reference in either
// the akvs:// or @Microsoft.KeyVault(SecretUri=...) format.
func IsSecretReference(s string) bool {
	return IsAzureKeyVaultSecret(s) || IsKeyVaultAppReference(s)
}

// validVaultHostSuffixes lists the known Azure Key Vault DNS suffixes.
// Used to validate SecretUri hostnames and prevent SSRF attacks via
// @Microsoft.KeyVault(SecretUri=https://evil.com/...) references.
var validVaultHostSuffixes = []string{
	".vault.azure.net",
	".vault.azure.cn",
	".vault.usgovcloudapi.net",
	".vault.microsoftazure.de",
	".managedhsm.azure.net",
}

// isValidVaultHost reports whether host is a known Azure Key Vault endpoint.
func isValidVaultHost(host string) bool {
	host = strings.ToLower(host)
	for _, suffix := range validVaultHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// KeyVaultAppReference represents a parsed @Microsoft.KeyVault(SecretUri=...) reference.
type KeyVaultAppReference struct {
	// VaultURL is the full vault URL (e.g., "https://my-vault.vault.azure.net").
	VaultURL string

	// VaultName is the vault name extracted from the host.
	VaultName string

	// SecretName is the name of the secret.
	SecretName string

	// SecretVersion is the specific version, or empty for latest.
	SecretVersion string
}

// ParseKeyVaultAppReference parses an @Microsoft.KeyVault(SecretUri=...) reference.
//
// Expected format:
//
//	@Microsoft.KeyVault(SecretUri=https://<vault>.vault.azure.net/secrets/<secret>[/<version>])
func ParseKeyVaultAppReference(ref string) (KeyVaultAppReference, error) {
	if !IsKeyVaultAppReference(ref) {
		return KeyVaultAppReference{}, fmt.Errorf("invalid @Microsoft.KeyVault reference: %s", ref)
	}

	inner := strings.TrimSpace(ref[len(keyVaultAppRefPrefix) : len(ref)-1])

	const secretURIPrefix = "SecretUri="
	if !strings.HasPrefix(inner, secretURIPrefix) {
		return KeyVaultAppReference{}, fmt.Errorf(
			"invalid @Microsoft.KeyVault reference %q: expected SecretUri= parameter", ref)
	}

	secretURI := strings.TrimSpace(inner[len(secretURIPrefix):])
	if secretURI == "" {
		return KeyVaultAppReference{}, fmt.Errorf(
			"invalid @Microsoft.KeyVault reference %q: SecretUri value must not be empty", ref)
	}

	u, err := url.Parse(secretURI)
	if err != nil {
		return KeyVaultAppReference{}, fmt.Errorf(
			"invalid @Microsoft.KeyVault reference %q: malformed SecretUri: %w", ref, err)
	}

	if u.Scheme != "https" {
		return KeyVaultAppReference{}, fmt.Errorf(
			"invalid @Microsoft.KeyVault reference %q: SecretUri must use https scheme", ref)
	}

	if u.Host == "" {
		return KeyVaultAppReference{}, fmt.Errorf(
			"invalid @Microsoft.KeyVault reference %q: SecretUri must include a host", ref)
	}

	if !isValidVaultHost(u.Host) {
		return KeyVaultAppReference{}, fmt.Errorf(
			"invalid @Microsoft.KeyVault reference %q: host %q is not a known Azure Key Vault endpoint", ref, u.Host)
	}

	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "secrets" {
		return KeyVaultAppReference{}, fmt.Errorf(
			"invalid @Microsoft.KeyVault reference %q: SecretUri path must be /secrets/<name>[/<version>]", ref)
	}

	secretName := parts[1]
	if secretName == "" {
		return KeyVaultAppReference{}, fmt.Errorf(
			"invalid @Microsoft.KeyVault reference %q: secret name must not be empty", ref)
	}

	var secretVersion string
	if len(parts) >= 3 && parts[2] != "" {
		secretVersion = parts[2]
	}

	vaultName := u.Host
	if idx := strings.Index(vaultName, "."); idx > 0 {
		vaultName = vaultName[:idx]
	}

	return KeyVaultAppReference{
		VaultURL:      fmt.Sprintf("https://%s", u.Host),
		VaultName:     vaultName,
		SecretName:    secretName,
		SecretVersion: secretVersion,
	}, nil
}

// ResolveSecretEnvironment resolves Key Vault secret references in a list of
// environment variables (in "KEY=VALUE" format). Any value that matches the
// akvs:// or @Microsoft.KeyVault(SecretUri=...) format is replaced with the
// resolved secret value. Non-secret values are passed through unchanged.
//
// Resolution failures for individual variables are logged. On failure the
// value is set to an empty string rather than leaking the raw reference to
// downstream consumers who would not know how to handle it.
func ResolveSecretEnvironment(
	ctx context.Context,
	kvService KeyVaultService,
	envVars []string,
	defaultSubscriptionId string,
) []string {
	if kvService == nil || defaultSubscriptionId == "" {
		return envVars
	}

	result := make([]string, len(envVars))
	for i, envVar := range envVars {
		eqIdx := strings.Index(envVar, "=")
		if eqIdx < 0 {
			result[i] = envVar
			continue
		}

		key := envVar[:eqIdx]
		value := envVar[eqIdx+1:]

		if !IsSecretReference(value) {
			result[i] = envVar
			continue
		}

		resolved, err := kvService.SecretFromKeyVaultReference(ctx, value, defaultSubscriptionId)
		if err != nil {
			log.Printf("warning: failed to resolve Key Vault reference for %s: %v", key, err)
			result[i] = key + "=" // Empty value — don't leak the raw reference
			continue
		}

		result[i] = key + "=" + resolved
	}

	// Sort for deterministic output.
	sort.Strings(result)

	return result
}
