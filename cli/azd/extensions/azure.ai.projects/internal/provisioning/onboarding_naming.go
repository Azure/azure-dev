// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"azure.ai.projects/internal/onboarding"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

type byoResolvedNames struct {
	AccountID, RegistryName, DeploymentID string
	ResourceGroupSupplied                 bool
}

type byoNamingScope struct {
	SubscriptionID  string `json:"subscriptionId"`
	ResourceGroupID string `json:"resourceGroupId"`
	Location        string `json:"location"`
	Salt            string `json:"salt"`
}

var officialResourceToken = regexp.MustCompile(`^[a-z2-7]{13}$`)

func (p *FoundryProvisioningProvider) resolveBYONames(
	ctx context.Context, source *templateSource,
) (byoResolvedNames, error) {
	var result byoResolvedNames
	values := map[string]string{}
	for _, key := range []string{"resourceGroupName", "location", "resourceTokenSalt", "foundryProjectName"} {
		value, ok := byoParameterValue(source, key).(string)
		if !ok || key != "resourceTokenSalt" && value == "" {
			return result, fmt.Errorf("effective %s must be a literal string, nonempty except resourceTokenSalt", key)
		}
		values[key] = value
	}
	if p.byoNames != nil {
		return p.byoNames(ctx, source)
	}
	if err := p.ensureCredential(ctx); err != nil {
		return result, err
	}
	options := onboarding.ARMOptions(p.armClientOptions)
	subscriptions, err := armsubscriptions.NewClient(p.credential, options)
	if err != nil {
		return result, err
	}
	subscription, err := subscriptions.Get(ctx, p.subID, nil)
	if err != nil {
		return result, fmt.Errorf("read exact subscription scope for native Foundry naming: %w", err)
	}
	if subscription.ID == nil || !strings.EqualFold(*subscription.ID, "/subscriptions/"+p.subID) {
		return result, fmt.Errorf("native Foundry naming requires the actual selected subscription ARM ID")
	}
	groupID := *subscription.ID + "/resourceGroups/" + values["resourceGroupName"]
	groups, err := armresources.NewResourceGroupsClient(p.subID, p.credential, options)
	if err != nil {
		return result, err
	}
	group, err := groups.Get(ctx, values["resourceGroupName"], nil)
	if err == nil {
		if group.ID == nil || !strings.EqualFold(*group.ID, groupID) {
			return result, fmt.Errorf("native Foundry naming requires the exact selected resource-group ARM ID")
		}
		groupID, result.ResourceGroupSupplied = *group.ID, true
	} else if !isNotFound(err) {
		return result, fmt.Errorf("read exact resource-group scope for native Foundry naming: %w", err)
	}
	token, err := evaluateBYOResourceToken(ctx, p.bicepCli(), byoNamingScope{
		SubscriptionID: *subscription.ID, ResourceGroupID: groupID,
		Location: values["location"], Salt: values["resourceTokenSalt"],
	})
	if err != nil {
		return result, err
	}
	result.AccountID = groupID + "/providers/Microsoft.CognitiveServices/accounts/cog-" + token
	result.RegistryName = "cr" + token
	result.DeploymentID = "/subscriptions/" + p.subID + "/providers/Microsoft.Resources/deployments/" + p.deploymentName()
	return result, nil
}

func evaluateBYOResourceToken(
	ctx context.Context, compiler bicepCompiler, scope byoNamingScope,
) (token string, err error) {
	dir, err := os.MkdirTemp("", "azd-foundry-naming-")
	if err != nil {
		return "", fmt.Errorf("create native naming evaluation directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	raw, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "scope.json"), raw, 0o600); err != nil {
		return "", err
	}
	const probe = "using none\n" +
		"var scope = loadJsonContent('./scope.json')\n" +
		"param resourceToken = empty(scope.salt)\n" +
		"  ? uniqueString(scope.subscriptionId, scope.resourceGroupId, scope.location)\n" +
		"  : uniqueString(scope.subscriptionId, scope.resourceGroupId, scope.location, scope.salt)\n"
	path := filepath.Join(dir, "naming.bicepparam")
	if err := os.WriteFile(path, []byte(probe), 0o600); err != nil {
		return "", err
	}
	compiled, err := compiler.BuildBicepParam(ctx, path, []string{"BICEP_PARAMETERS_OVERRIDES={}"})
	if err != nil {
		return "", fmt.Errorf("evaluate native Foundry/ACR names with azd-managed Bicep: %w", err)
	}
	var envelope struct {
		ParametersJSON string `json:"parametersJson"`
	}
	if err := json.Unmarshal([]byte(compiled.Compiled), &envelope); err != nil {
		return "", err
	}
	var parameters struct {
		Parameters map[string]struct {
			Value string `json:"value"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(envelope.ParametersJSON), &parameters); err != nil {
		return "", err
	}
	token = parameters.Parameters["resourceToken"].Value
	if !officialResourceToken.MatchString(token) {
		return "", fmt.Errorf("Bicep returned an invalid native resource token")
	}
	return token, nil
}
