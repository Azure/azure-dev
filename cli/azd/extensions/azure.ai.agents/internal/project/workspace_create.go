// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/azure"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const (
	amlWorkspaceAPIVersion = "2024-04-01"
	storageAPIVersion      = "2023-01-01"
	keyVaultAPIVersion     = "2023-07-01"
)

// ensurePromptWorkspaceExists verifies that an AML workspace named
// settings.Workspace exists in the target resource group, and creates one—
// along with a storage account and key vault as prerequisites—when it is absent.
//
// The managed prompt-agent harness API routes every operation through:
//
//	.../providers/Microsoft.MachineLearningServices/workspaces/{name}/...
//
// so the workspace must exist as an ARM resource before agents can be registered.
//
// Both AZURE_LOCATION and AZURE_TENANT_ID must be present in env.
// The function is idempotent: running it twice with the same settings produces the
// same storage/keyvault/workspace names and skips re-creation.
func ensurePromptWorkspaceExists(
	ctx context.Context,
	settings *PromptAgentSettings,
	env map[string]string,
	progress azdext.ProgressReporter,
) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("workspace provisioning panicked: %v", r)
		}
	}()

	if settings == nil {
		return nil
	}

	cred := promptCredential()
	if cred == nil {
		return fmt.Errorf("no credential available to provision the AML workspace")
	}

	client, err := armresources.NewClient(settings.SubscriptionID, cred, azure.NewArmClientOptions())
	if err != nil {
		return fmt.Errorf("creating ARM client: %w", err)
	}

	wsResourceID := amlWorkspaceResourceID(settings.SubscriptionID, settings.ResourceGroup, settings.Workspace)

	// Fast-path: workspace already exists.
	if _, err := client.GetByID(ctx, wsResourceID, amlWorkspaceAPIVersion, nil); err == nil {
		return nil
	} else if respErr, ok := errors.AsType[*azcore.ResponseError](err); !ok || respErr.StatusCode != 404 {
		return fmt.Errorf("checking AML workspace existence: %w", err)
	}

	location := strings.ToLower(strings.TrimSpace(env["AZURE_LOCATION"]))
	if location == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"AZURE_LOCATION is required to provision the AML workspace",
			"run 'azd env set AZURE_LOCATION <region>' and re-deploy",
		)
	}

	tenantID := strings.TrimSpace(env["AZURE_TENANT_ID"])
	if tenantID == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"AZURE_TENANT_ID is required to provision the AML workspace's key vault",
			"run 'azd env set AZURE_TENANT_ID <tenantId>' and re-deploy",
		)
	}

	// Suffix is deterministic so repeated deploys reuse the same dependencies.
	suffix := amlDependencyNameSuffix(settings.SubscriptionID, settings.ResourceGroup, settings.Workspace)

	if progress != nil {
		progress(fmt.Sprintf("Provisioning storage account for workspace %q", settings.Workspace))
	}
	storageID, err := ensureStorageAccountForWorkspace(ctx, client, settings, location, suffix)
	if err != nil {
		return fmt.Errorf("provisioning storage account: %w", err)
	}

	if progress != nil {
		progress(fmt.Sprintf("Provisioning key vault for workspace %q", settings.Workspace))
	}
	kvID, err := ensureKeyVaultForWorkspace(ctx, client, settings, location, suffix, tenantID)
	if err != nil {
		return fmt.Errorf("provisioning key vault: %w", err)
	}

	if progress != nil {
		progress(fmt.Sprintf("Creating AML workspace %q", settings.Workspace))
	}
	if err := createAMLWorkspace(ctx, client, wsResourceID, location, storageID, kvID); err != nil {
		return fmt.Errorf("creating AML workspace: %w", err)
	}
	if progress != nil {
		progress(fmt.Sprintf("AML workspace %q is ready", settings.Workspace))
	}
	return nil
}

func amlWorkspaceResourceID(subscriptionID, resourceGroup, name string) string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.MachineLearningServices/workspaces/%s",
		subscriptionID, resourceGroup, name,
	)
}

// amlDependencyNameSuffix returns 8 lower-hex characters derived deterministically
// from the given strings.  Storage account and key vault names are built from this
// suffix so repeated deploys reuse the same backing resources.
func amlDependencyNameSuffix(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = fmt.Fprintf(h, "%s\x00", p)
	}
	return hex.EncodeToString(h.Sum(nil))[:8]
}

// ensureStorageAccountForWorkspace idempotently creates (or reuses) the storage
// account that AML workspace creation requires.
func ensureStorageAccountForWorkspace(
	ctx context.Context,
	client *armresources.Client,
	settings *PromptAgentSettings,
	location, suffix string,
) (string, error) {
	// Storage account names: max 24 chars, lowercase alphanumeric only.
	name := "st" + suffix // "st" + 8 hex chars = 10 chars
	resourceID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Storage/storageAccounts/%s",
		settings.SubscriptionID, settings.ResourceGroup, name,
	)
	if _, err := client.GetByID(ctx, resourceID, storageAPIVersion, nil); err == nil {
		return resourceID, nil // already exists
	}
	skuName := "Standard_LRS"
	kind := "StorageV2"
	body := armresources.GenericResource{
		Location: &location,
		Kind:     &kind,
		SKU:      &armresources.SKU{Name: &skuName},
		Properties: map[string]interface{}{
			"supportsHttpsTrafficOnly": true,
			"accessTier":               "Hot",
		},
	}
	poller, err := client.BeginCreateOrUpdateByID(ctx, resourceID, storageAPIVersion, body, nil)
	if err != nil {
		return "", err
	}
	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		return "", err
	}
	return resourceID, nil
}

// ensureKeyVaultForWorkspace idempotently creates (or reuses) the key vault that
// AML workspace creation requires.
func ensureKeyVaultForWorkspace(
	ctx context.Context,
	client *armresources.Client,
	settings *PromptAgentSettings,
	location, suffix, tenantID string,
) (string, error) {
	// Key vault names: 3–24 chars, alphanumeric + hyphens.
	name := "kv-" + suffix // "kv-" + 8 hex chars = 11 chars
	resourceID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.KeyVault/vaults/%s",
		settings.SubscriptionID, settings.ResourceGroup, name,
	)
	if _, err := client.GetByID(ctx, resourceID, keyVaultAPIVersion, nil); err == nil {
		return resourceID, nil // already exists
	}
	body := armresources.GenericResource{
		Location: &location,
		Properties: map[string]interface{}{
			"sku":              map[string]interface{}{"family": "A", "name": "standard"},
			"tenantId":         tenantID,
			"accessPolicies":   []interface{}{},
			"enableSoftDelete": true,
		},
	}
	poller, err := client.BeginCreateOrUpdateByID(ctx, resourceID, keyVaultAPIVersion, body, nil)
	if err != nil {
		return "", err
	}
	if _, err = poller.PollUntilDone(ctx, nil); err != nil {
		return "", err
	}
	return resourceID, nil
}

// createAMLWorkspace creates the Microsoft.MachineLearningServices/workspaces
// resource.  It is designed to be called AFTER the prerequisite storage account
// and key vault have been created.
func createAMLWorkspace(
	ctx context.Context,
	client *armresources.Client,
	workspaceResourceID, location, storageID, kvID string,
) error {
	identityType := armresources.ResourceIdentityTypeSystemAssigned
	body := armresources.GenericResource{
		Location: &location,
		Identity: &armresources.Identity{Type: &identityType},
		Properties: map[string]interface{}{
			"storageAccount": storageID,
			"keyVault":       kvID,
		},
	}
	poller, err := client.BeginCreateOrUpdateByID(ctx, workspaceResourceID, amlWorkspaceAPIVersion, body, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return err
}
