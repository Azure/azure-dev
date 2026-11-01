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

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/onboarding"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
)

type byoSupportingNames struct {
	StorageID, HostingID, WorkloadID, RegistryID string
}

type byoNamingScope struct {
	SubscriptionID  string `json:"subscriptionId"`
	ResourceGroupID string `json:"resourceGroupId"`
	Location        string `json:"location"`
	Salt            string `json:"salt"`
}

var officialResourceToken = regexp.MustCompile(`^[a-z2-7]{13}$`)

func validateBYOSupportingTemplate(template map[string]any) error {
	raw, err := synthesis.ARMTemplate()
	if err != nil {
		return fmt.Errorf("load supporting-resource preflight template: %w", err)
	}
	var standard map[string]any
	if err := json.Unmarshal(raw, &standard); err != nil {
		return fmt.Errorf("decode supporting-resource preflight template: %w", err)
	}
	expected, err := onboarding.TemplateDigest(standard)
	if err != nil {
		return err
	}
	actual, err := onboarding.TemplateDigest(template)
	if err != nil {
		return err
	}
	if actual != expected {
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"supplied-resource preflight requires the standard BYO AKS deployment graph",
			"re-eject the infrastructure templates; customize supported deployment parameters instead of "+
				"resource naming, creation or authorization expressions")
	}
	return nil
}

func (p *FoundryProvisioningProvider) resolveBYOSupportingNames(
	ctx context.Context, source *templateSource, groupExists bool,
) (byoSupportingNames, error) {
	group := byoParameterValue(source, "resourceGroupName").(string)
	location := byoParameterValue(source, "location").(string)
	salt := byoParameterValue(source, "resourceTokenSalt").(string)
	subscriptions, err := armsubscriptions.NewClient(p.credential, byoARMOptions(p.armClientOptions))
	if err != nil {
		return byoSupportingNames{}, err
	}
	subscription, err := subscriptions.Get(ctx, p.subID, nil)
	if err != nil {
		return byoSupportingNames{}, fmt.Errorf("read exact subscription scope for default resource naming: %w", err)
	}
	subscriptionID := ""
	if subscription.ID != nil {
		subscriptionID = *subscription.ID
	}
	if !strings.EqualFold(subscriptionID, "/subscriptions/"+p.subID) {
		return byoSupportingNames{}, fmt.Errorf("default resource naming requires the actual selected subscription ARM ID")
	}
	groupID := subscriptionID + "/resourceGroups/" + group
	if groupExists {
		groups, err := armresources.NewResourceGroupsClient(p.subID, p.credential, byoARMOptions(p.armClientOptions))
		if err != nil {
			return byoSupportingNames{}, err
		}
		result, err := groups.Get(ctx, group, nil)
		if err != nil {
			return byoSupportingNames{}, fmt.Errorf("read exact resource-group scope for default resource naming: %w", err)
		}
		if result.ID == nil || !strings.EqualFold(*result.ID, groupID) {
			return byoSupportingNames{}, fmt.Errorf(
				"default resource naming requires the actual selected resource-group ARM ID")
		}
		groupID = *result.ID
	}
	token, err := evaluateBYOResourceToken(ctx, p.bicepCli(), byoNamingScope{
		SubscriptionID: subscriptionID, ResourceGroupID: groupID, Location: location, Salt: salt,
	})
	if err != nil {
		return byoSupportingNames{}, err
	}
	return byoSupportingNames{
		StorageID:  groupID + "/providers/Microsoft.Storage/storageAccounts/st" + token,
		HostingID:  groupID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cog-" + token + "-hosting-mi",
		WorkloadID: groupID + "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cog-" + token + "-workload-mi",
		RegistryID: groupID + "/providers/Microsoft.ContainerRegistry/registries/cr" + token,
	}, nil
}

func evaluateBYOPlanFunction(
	ctx context.Context, compiler bicepCompiler, name string, args []string,
) (value string, err error) {
	if name != "uniquestring" && name != "guid" {
		return "", fmt.Errorf("unsupported local Bicep plan function")
	}
	if name == "uniquestring" {
		name = "uniqueString"
	}
	dir, err := os.MkdirTemp("", "azd-foundry-plan-")
	if err != nil {
		return "", fmt.Errorf("create Bicep plan evaluation directory: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	raw, err := json.Marshal(args)
	if err != nil {
		return "", fmt.Errorf("encode Bicep plan arguments: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "arguments.json"), raw, 0o600); err != nil {
		return "", fmt.Errorf("write Bicep plan arguments: %w", err)
	}
	arguments := make([]string, len(args))
	for i := range args {
		arguments[i] = fmt.Sprintf("arguments[%d]", i)
	}
	probe := "using none\nvar arguments = loadJsonContent('./arguments.json')\nparam result = " +
		name + "(" + strings.Join(arguments, ", ") + ")\n"
	path := filepath.Join(dir, "plan.bicepparam")
	if err := os.WriteFile(path, []byte(probe), 0o600); err != nil {
		return "", fmt.Errorf("write local Bicep plan expression: %w", err)
	}
	result, err := compiler.BuildBicepParam(ctx, path, []string{"BICEP_PARAMETERS_OVERRIDES={}"})
	if err != nil {
		return "", fmt.Errorf("evaluate template naming with azd-managed Bicep: %w", err)
	}
	var envelope struct {
		ParametersJSON string `json:"parametersJson"`
	}
	if err := json.Unmarshal([]byte(result.Compiled), &envelope); err != nil {
		return "", fmt.Errorf("decode Bicep plan evaluation: %w", err)
	}
	var parameters struct {
		Parameters map[string]struct {
			Value string `json:"value"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(envelope.ParametersJSON), &parameters); err != nil {
		return "", fmt.Errorf("decode Bicep plan parameters: %w", err)
	}
	value = parameters.Parameters["result"].Value
	if value == "" || name == "uniqueString" && !officialResourceToken.MatchString(value) {
		return "", fmt.Errorf("Bicep returned an invalid local plan value")
	}
	return value, nil
}

func evaluateBYOResourceToken(
	ctx context.Context, compiler bicepCompiler, scope byoNamingScope,
) (token string, err error) {
	dir, err := os.MkdirTemp("", "azd-foundry-naming-")
	if err != nil {
		return "", fmt.Errorf("create official Foundry naming evaluation directory: %w", err)
	}
	defer func() {
		err = errors.Join(err, os.RemoveAll(dir))
	}()
	raw, err := json.Marshal(scope)
	if err != nil {
		return "", fmt.Errorf("encode official Foundry naming scope: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scope.json"), raw, 0o600); err != nil {
		return "", fmt.Errorf("write official Foundry naming scope: %w", err)
	}
	const probe = "using none\n" +
		"var scope = loadJsonContent('./scope.json')\n" +
		"param resourceToken = empty(scope.salt)\n" +
		"  ? uniqueString(scope.subscriptionId, scope.resourceGroupId, scope.location)\n" +
		"  : uniqueString(scope.subscriptionId, scope.resourceGroupId, scope.location, scope.salt)\n"
	path := filepath.Join(dir, "naming.bicepparam")
	if err := os.WriteFile(path, []byte(probe), 0o600); err != nil {
		return "", fmt.Errorf("write official Foundry naming expression: %w", err)
	}
	compiled, err := compiler.BuildBicepParam(ctx, path, []string{"BICEP_PARAMETERS_OVERRIDES={}"})
	if err != nil {
		return "", fmt.Errorf("evaluate official Foundry naming with azd-managed Bicep: %w", err)
	}
	var envelope struct {
		ParametersJSON string `json:"parametersJson"`
	}
	if err := json.Unmarshal([]byte(compiled.Compiled), &envelope); err != nil {
		return "", fmt.Errorf("decode official Foundry naming evaluation: %w", err)
	}
	var parameters struct {
		Parameters map[string]struct {
			Value string `json:"value"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(envelope.ParametersJSON), &parameters); err != nil {
		return "", fmt.Errorf("decode official Foundry naming parameters: %w", err)
	}
	token = parameters.Parameters["resourceToken"].Value
	if !officialResourceToken.MatchString(token) {
		return "", fmt.Errorf("official Bicep returned an invalid Foundry resource token")
	}
	return token, nil
}
