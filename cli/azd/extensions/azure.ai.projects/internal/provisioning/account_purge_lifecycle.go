// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"azure.ai.projects/internal/exterrors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

const accountPurgeConfigKey = "extensions.ai-projects.accountPurge"

type accountPurgeRecord struct {
	Version         int                  `json:"version"`
	EnvironmentName string               `json:"environmentName"`
	IsLayer         bool                 `json:"isLayer"`
	LayerName       string               `json:"layerName"`
	ResourceGroupID string               `json:"resourceGroupId"`
	Accounts        []accountPurgeTarget `json:"accounts"`
}

type accountPurgeTarget struct {
	ResourceID string `json:"resourceId"`
	Name       string `json:"name"`
	Location   string `json:"location"`
}

func (p *FoundryProvisioningProvider) validateAccountPurgeRecord(record *accountPurgeRecord) error {
	if record.Version != 1 || p.envName == "" || record.EnvironmentName != p.envName ||
		record.IsLayer != p.isLayer || record.LayerName != p.infraName ||
		p.isLayer && p.infraName == "" || p.brownfieldEndpoint != "" ||
		!resourceGroupIDMatches(record.ResourceGroupID, p.subID, p.rgName) || len(record.Accounts) == 0 {
		return errors.New("account purge recovery state does not match the current environment, infrastructure layer, and resource group")
	}
	for _, account := range record.Accounts {
		if err := p.validateAccountPurgeTarget(account); err != nil {
			return err
		}
	}
	return nil
}

func (p *FoundryProvisioningProvider) validateAccountPurgeTarget(account accountPurgeTarget) error {
	id, err := arm.ParseResourceID(account.ResourceID)
	if err != nil || !strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts") ||
		!strings.EqualFold(id.SubscriptionID, p.subID) || !strings.EqualFold(id.ResourceGroupName, p.rgName) ||
		!strings.EqualFold(id.Name, account.Name) || strings.TrimSpace(account.Name) == "" ||
		strings.TrimSpace(account.Location) == "" || strings.ContainsAny(account.Name+account.Location, `/\?#%`) {
		return errors.New("account purge recovery state contains an invalid account identity or location")
	}
	return nil
}

func (p *FoundryProvisioningProvider) loadAccountPurge(ctx context.Context) error {
	p.accountPurge = nil
	if p.azdClient == nil {
		return nil
	}
	response, err := p.azdClient.Environment().GetConfig(ctx, &azdext.GetConfigRequest{
		EnvName: p.envName, Path: accountPurgeConfigKey,
	})
	if err != nil {
		return fmt.Errorf("read account purge recovery state: %w", err)
	}
	if !response.GetFound() {
		return nil
	}
	var record accountPurgeRecord
	decoder := json.NewDecoder(bytes.NewReader(response.GetValue()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return fmt.Errorf("invalid account purge recovery state: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid account purge recovery state: expected one object")
	}
	if err := p.validateAccountPurgeRecord(&record); err != nil {
		return err
	}
	p.accountPurge = &record
	return nil
}

func (p *FoundryProvisioningProvider) persistAccountPurge(
	ctx context.Context, accounts []*armcognitiveservices.Account,
) error {
	record := accountPurgeRecord{
		Version: 1, EnvironmentName: p.envName, IsLayer: p.isLayer, LayerName: p.infraName,
		ResourceGroupID: "/subscriptions/" + p.subID + "/resourceGroups/" + p.rgName,
	}
	if p.accountPurge != nil {
		record = *p.accountPurge
	}
	record.Accounts = nil
	appendTarget := func(target accountPurgeTarget) {
		if !slices.ContainsFunc(record.Accounts, func(saved accountPurgeTarget) bool {
			return strings.EqualFold(saved.ResourceID, target.ResourceID) &&
				strings.EqualFold(saved.Location, target.Location)
		}) {
			record.Accounts = append(record.Accounts, target)
		}
	}
	if p.accountPurge != nil {
		for _, account := range p.accountPurge.Accounts {
			appendTarget(account)
		}
	}
	for _, account := range collectPurgeableAccounts(accounts) {
		target := accountPurgeTarget{
			ResourceID: account.resourceID, Name: account.name, Location: account.location,
		}
		if err := p.validateAccountPurgeTarget(target); err != nil {
			return err
		}
		appendTarget(target)
	}
	if err := p.validateAccountPurgeRecord(&record); err != nil {
		return err
	}
	if p.accountPurge == nil || !slices.Equal(record.Accounts, p.accountPurge.Accounts) {
		if p.azdClient == nil {
			return errors.New("cannot persist account purge recovery state without an azd environment")
		}
		data, err := json.Marshal(&record)
		if err != nil {
			return fmt.Errorf("encode account purge recovery state: %w", err)
		}
		if _, err := p.azdClient.Environment().SetConfig(ctx, &azdext.SetConfigRequest{
			EnvName: p.envName, Path: accountPurgeConfigKey, Value: data,
		}); err != nil {
			return fmt.Errorf("persist account purge recovery state: %w", err)
		}
		p.accountPurge = &record
	}
	// A recovered create-mode registry binding must also survive deletion of
	// its Project, including when the registry-reuse preview is disabled.
	return p.persistRecoveredACRBinding(ctx)
}

func (p *FoundryProvisioningProvider) recordedPurgeAccounts() []purgeableAccount {
	accounts := make([]purgeableAccount, 0, len(p.accountPurge.Accounts))
	for _, account := range p.accountPurge.Accounts {
		accounts = append(accounts, purgeableAccount{
			resourceID: account.ResourceID, name: account.Name, location: account.Location,
		})
	}
	return accounts
}

func (p *FoundryProvisioningProvider) completeAccountCleanup(
	ctx context.Context,
	options *azdext.ProvisioningDestroyOptions,
	progress grpcbroker.ProgressFunc,
	accounts []purgeableAccount,
) (*azdext.ProvisioningDestroyResult, error) {
	if options.GetPurge() && len(accounts) > 0 {
		if err := p.purgeCognitiveAccounts(ctx, progress, accounts); err != nil {
			return nil, err
		}
	}
	result, err := p.completeDestroy(ctx)
	if err != nil {
		return nil, err
	}
	if p.accountPurge == nil {
		return result, nil
	}
	if _, err := p.azdClient.Environment().UnsetConfig(ctx, &azdext.UnsetConfigRequest{
		EnvName: p.envName, Path: accountPurgeConfigKey,
	}); err != nil {
		return nil, fmt.Errorf("clear completed account purge recovery state: %w", err)
	}
	p.accountPurge = nil
	return result, nil
}

func (p *FoundryProvisioningProvider) confirmPurgedAccountAbsent(
	ctx context.Context, client *armcognitiveservices.DeletedAccountsClient, account purgeableAccount, purgeErr error,
) (bool, error) {
	if p.accountPurge == nil || !isNotFound(purgeErr) {
		return false, nil
	}
	_, err := client.Get(ctx, account.location, p.rgName, account.name, nil)
	if response, ok := errors.AsType[*azcore.ResponseError](err); ok &&
		response.StatusCode == http.StatusNotFound && response.ErrorCode == "ResourceNotFound" {
		return true, nil
	}
	if err != nil {
		return false, exterrors.ServiceFromAzure(err, exterrors.OpCognitiveDeletedAccountGet)
	}
	return false, nil
}
