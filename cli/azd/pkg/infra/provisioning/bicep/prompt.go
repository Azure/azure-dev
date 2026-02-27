// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/ai"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/password"
	"github.com/azure/azure-dev/cli/azd/pkg/prompt"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

// promptDialogItemForParameter builds the input.PromptDialogItem for the given required parameter.
func (p *BicepProvider) promptDialogItemForParameter(
	key string,
	param azure.ArmTemplateParameterDefinition,
) input.PromptDialogItem {
	help, _ := param.Description()
	paramType := provisioning.ParameterTypeFromArmType(param.Type)

	var dialogItem input.PromptDialogItem
	dialogItem.ID = key
	dialogItem.DisplayName = key
	dialogItem.Required = true

	if help != "" {
		dialogItem.Description = to.Ptr(help)
	}

	if paramType == provisioning.ParameterTypeBoolean {
		dialogItem.Kind = "select"
		dialogItem.Choices = []input.PromptDialogChoice{{Value: "true"}, {Value: "false"}}
	} else if param.AllowedValues != nil {
		dialogItem.Kind = "select"
		for _, v := range *param.AllowedValues {
			dialogItem.Choices = append(dialogItem.Choices, input.PromptDialogChoice{Value: fmt.Sprintf("%v", v)})
		}
	} else if param.Secure() {
		dialogItem.Kind = "password"
	} else {
		dialogItem.Kind = "string"
	}

	return dialogItem
}

func autoGenerate(parameter string, azdMetadata azure.AzdMetadata) (string, error) {
	if azdMetadata.AutoGenerateConfig == nil {
		return "", fmt.Errorf("auto generation metadata config is missing for parameter '%s'", parameter)
	}
	genValue, err := password.Generate(password.GenerateConfig{
		Length:     azdMetadata.AutoGenerateConfig.Length,
		NoLower:    azdMetadata.AutoGenerateConfig.NoLower,
		NoUpper:    azdMetadata.AutoGenerateConfig.NoUpper,
		NoNumeric:  azdMetadata.AutoGenerateConfig.NoNumeric,
		NoSpecial:  azdMetadata.AutoGenerateConfig.NoSpecial,
		MinLower:   azdMetadata.AutoGenerateConfig.MinLower,
		MinUpper:   azdMetadata.AutoGenerateConfig.MinUpper,
		MinNumeric: azdMetadata.AutoGenerateConfig.MinNumeric,
		MinSpecial: azdMetadata.AutoGenerateConfig.MinSpecial,
	})
	if err != nil {
		return "", err
	}
	return genValue, nil
}

// locationsWithQuotaFor finds locations that have sufficient quota for the given usage requirements.
//
// The quotaFor parameter uses the Bicep metadata format: "UsageName" or "UsageName, Capacity".
// An implicit requirement for "OpenAI.S0.AccountCount" with capacity 2 is always included.
func (a *BicepProvider) locationsWithQuotaFor(
	ctx context.Context, subId string, locations []string, quotaFor []string) ([]string, error) {
	if a.aiModelService == nil {
		return nil, fmt.Errorf("AI model service is not configured")
	}

	// Always require minimum S0 account quota
	requirements := []ai.QuotaRequirement{
		{UsageName: "OpenAI.S0.AccountCount", MinCapacity: 2},
	}

	for _, definedUsageName := range quotaFor {
		usageDetails, err := usageNameDetailsFromString(definedUsageName)
		if err != nil {
			return nil, fmt.Errorf("parsing quota '%s': %w", definedUsageName, err)
		}
		requirements = append(requirements, ai.QuotaRequirement{
			UsageName:   usageDetails.UsageName,
			MinCapacity: usageDetails.Capacity,
		})
	}

	results, err := a.aiModelService.ListLocationsWithQuota(ctx, subId, locations, requirements)
	if err != nil {
		return nil, fmt.Errorf("getting locations with quota: %w", err)
	}

	if len(results) == 0 {
		formattedQuota := make([]string, len(quotaFor))
		for i, quota := range quotaFor {
			f, err := usageNameDetailsFromString(quota)
			if err != nil {
				return nil, fmt.Errorf("parsing quota '%s': %w", quota, err)
			}
			formattedQuota[i] = fmt.Sprintf("%s ( Cap: %.0f )", f.UsageName, f.Capacity)
		}
		return nil, fmt.Errorf(
			"no location found with enough quota for %s",
			ux.ListAsText(formattedQuota))
	}
	return results, nil
}

// resolveModelSkuUsageName resolves a model name to its full SKU usage name by querying the
// AI model catalog. It filters SKUs to only standard-tier types (GlobalStandard, DataZoneStandard,
// Standard), excluding provisioned, batch, and developer SKUs which have fundamentally different
// billing and usage models. If GlobalStandard is available, it is selected automatically as it
// offers the highest quota and broadest availability. Otherwise, the user is prompted to choose
// among the remaining standard-tier SKUs.
func (p *BicepProvider) resolveModelSkuUsageName(
	ctx context.Context,
	subId string,
	modelName string,
) (string, error) {
	if p.aiModelService == nil {
		return "", fmt.Errorf("AI model service is not configured")
	}

	models, err := p.aiModelService.ListModels(ctx, subId, nil)
	if err != nil {
		return "", fmt.Errorf("listing AI models: %w", err)
	}

	// Find the target model and collect unique SKUs across all versions
	type skuInfo struct {
		Name      string
		UsageName string
	}
	var allSkus []skuInfo
	seen := map[string]struct{}{}
	modelFound := false

	for _, model := range models {
		if model.Name != modelName {
			continue
		}
		modelFound = true
		for _, version := range model.Versions {
			for _, sku := range version.Skus {
				if _, ok := seen[sku.UsageName]; !ok {
					seen[sku.UsageName] = struct{}{}
					allSkus = append(allSkus, skuInfo{
						Name:      sku.Name,
						UsageName: sku.UsageName,
					})
				}
			}
		}
		break
	}

	if !modelFound {
		return "", fmt.Errorf("model '%s' not found in the AI model catalog", modelName)
	}
	if len(allSkus) == 0 {
		return "", fmt.Errorf("no SKUs found for model '%s'", modelName)
	}

	// Filter to standard-tier SKUs only.
	// Provisioned (PTU-based), Batch (async 24-hr), and Developer SKUs have fundamentally
	// different billing and usage models that are not suitable for typical deployments.
	standardTierSkus := make([]skuInfo, 0, len(allSkus))
	for _, s := range allSkus {
		switch s.Name {
		case "GlobalStandard", "DataZoneStandard", "Standard":
			standardTierSkus = append(standardTierSkus, s)
		}
	}

	// If no standard-tier SKUs are available, fall back to all SKUs so the user
	// can still proceed (they may have a provisioned deployment in mind).
	candidates := standardTierSkus
	if len(candidates) == 0 {
		candidates = allSkus
	}

	if len(candidates) == 1 {
		return candidates[0].UsageName, nil
	}

	// Auto-select GlobalStandard if available — it has the highest default quota
	// and broadest availability, recommended as the starting point for most workloads.
	for _, s := range candidates {
		if s.Name == "GlobalStandard" {
			return s.UsageName, nil
		}
	}

	// Multiple candidates without GlobalStandard — prompt the user to select one
	options := make([]string, len(candidates))
	for i, s := range candidates {
		options[i] = fmt.Sprintf("%s (%s)", s.Name, s.UsageName)
	}

	choice, err := p.console.Select(ctx, input.ConsoleOptions{
		Message: fmt.Sprintf("Select a deployment SKU for model '%s':", modelName),
		Options: options,
	})
	if err != nil {
		return "", err
	}

	return candidates[choice].UsageName, nil
}

// resolveUsageNamesWithReferences resolves usageName entries that contain $(p:...) parameter
// references. For referenced entries, the resolved value is treated as "modelName, capacity"
// and the model name is resolved to its full SKU usage name via the AI model catalog.
// Constant (non-referenced) entries are passed through unchanged.
func (p *BicepProvider) resolveUsageNamesWithReferences(
	ctx context.Context,
	usageNames []string,
	resolvedValues map[string]any,
) ([]string, error) {
	result := make([]string, 0, len(usageNames))
	for _, un := range usageNames {
		if !azure.HasParamReferences(un) {
			// Constant entry — use as-is (existing behavior)
			result = append(result, un)
			continue
		}

		// Resolve $(p:...) references
		resolved, err := azure.ResolveParamReferences(un, resolvedValues)
		if err != nil {
			return nil, fmt.Errorf("resolving usageName references: %w", err)
		}

		// Parse the resolved string as "modelName, capacity" or "modelName"
		parts := strings.SplitN(resolved, ",", 2)
		modelName := strings.TrimSpace(parts[0])
		capacityStr := ""
		if len(parts) == 2 {
			capacityStr = strings.TrimSpace(parts[1])
		}

		// Resolve the model name to its full SKU usage name
		skuUsageName, err := p.resolveModelSkuUsageName(ctx, p.env.GetSubscriptionId(), modelName)
		if err != nil {
			return nil, fmt.Errorf("resolving SKU for model '%s': %w", modelName, err)
		}

		// Build the final "UsageName, capacity" string
		if capacityStr != "" {
			result = append(result, fmt.Sprintf("%s, %s", skuUsageName, capacityStr))
		} else {
			result = append(result, skuUsageName)
		}
	}
	return result, nil
}

type usageNameDetails struct {
	UsageName string
	Capacity  float64
}

func usageNameDetailsFromString(usageName string) (usageNameDetails, error) {
	usage := strings.TrimSpace(usageName)
	if len(usage) == 0 {
		return usageNameDetails{}, fmt.Errorf("empty usage name")
	}
	parts := strings.Split(usage, ",")
	if len(parts) == 1 {
		return usageNameDetails{
			UsageName: usage,
			Capacity:  1,
		}, nil
	}
	if len(parts) != 2 {
		return usageNameDetails{}, fmt.Errorf("invalid usage name format '%s'", usage)
	}
	usageName = strings.TrimSpace(parts[0])
	capacity, err := strconv.ParseFloat(strings.Trim(parts[1], " "), 64)
	if err != nil {
		return usageNameDetails{}, fmt.Errorf("invalid capacity '%s': %w", parts[1], err)
	}
	if capacity <= 0 {
		return usageNameDetails{}, fmt.Errorf("invalid capacity '%.0f': must be greater than 0", capacity)
	}
	return usageNameDetails{
		UsageName: usageName,
		Capacity:  capacity,
	}, nil
}

func (p *BicepProvider) promptForParameter(
	ctx context.Context,
	key string,
	param azure.ArmTemplateParameterDefinition,
	mappedToAzureLocationParams []string,
	resolvedValues map[string]any,
) (any, error) {
	securedParam := "parameter"
	isSecuredParam := param.Secure()
	if isSecuredParam {
		securedParam = "secured parameter"
	}
	msg := fmt.Sprintf("Enter a value for the '%s' infrastructure %s:", key, securedParam)
	help, _ := param.Description()
	azdMetadata, _ := param.AzdMetadata()
	paramType := provisioning.ParameterTypeFromArmType(param.Type)

	var value any

	if paramType == provisioning.ParameterTypeString &&
		azdMetadata.Type != nil && *azdMetadata.Type == azure.AzdMetadataTypeLocation {

		// when more than one parameter is mapped to AZURE_LOCATION and AZURE_LOCATION is not set in the environment,
		// AZD will prompt just once and immediately set the value in the .env for the next parameter to re-use the value
		paramIsMappedToAzureLocation := slices.Contains(mappedToAzureLocationParams, key)
		valueFromEnv, valueDefinedInEnv := p.env.LookupEnv(environment.LocationEnvVarName)
		if paramIsMappedToAzureLocation && valueDefinedInEnv {
			return valueFromEnv, nil
		}

		// location can be combined with allowedValues and with usageName metadata
		// allowedValues == nil => all locations are allowed
		// allowedValues != nil => only the locations in the allowedValues are allowed
		// usageName != nil => the usageName is validated for quota for each allowed location (this is for Ai models),
		//                     reducing the allowed locations to only those that have quota available
		// usageName == nil => No quota validation is done
		var allowedLocations []string
		if param.AllowedValues != nil {
			allowedLocations = make([]string, len(*param.AllowedValues))
			for i, option := range *param.AllowedValues {
				allowedLocations[i] = option.(string)
			}
		}
		if len(azdMetadata.UsageName) > 0 {
			// Show a user-friendly spinner while resolving quota availability
			p.console.ShowSpinner(ctx, "Checking available quota for location selection", input.Step)

			// Resolve any $(p:...) parameter references in usageName entries.
			// Referenced entries are resolved to full SKU usage names via the AI model catalog.
			resolvedUsageNames, err := p.resolveUsageNamesWithReferences(
				ctx, azdMetadata.UsageName, resolvedValues)
			if err != nil {
				p.console.StopSpinner(ctx, "", input.Step)
				return nil, fmt.Errorf("resolving usageName for parameter '%s': %w", key, err)
			}
			withQuotaLocations, err := p.locationsWithQuotaFor(
				ctx, p.env.GetSubscriptionId(), allowedLocations, resolvedUsageNames)
			if err != nil {
				p.console.StopSpinner(ctx, "", input.Step)
				return nil, fmt.Errorf("getting locations with quota: %w", err)
			}
			allowedLocations = withQuotaLocations

			p.console.StopSpinner(ctx, "", input.Step)
		}

		location, err := p.prompters.PromptLocation(
			ctx, p.env.GetSubscriptionId(), msg, func(loc account.Location) bool {
				return locationParameterFilterImpl(allowedLocations, loc)
			}, defaultPromptValue(param))
		if err != nil {
			return nil, err
		}

		if paramIsMappedToAzureLocation && !valueDefinedInEnv {
			// set the location in the environment variable
			p.env.SetLocation(location)
			if err := p.envManager.Save(ctx, p.env); err != nil {
				return nil, fmt.Errorf("setting location in environment variable: %w", err)
			}
		}
		value = location
	} else if paramType == provisioning.ParameterTypeString &&
		azdMetadata.Type != nil &&
		*azdMetadata.Type == azure.AzdMetadataTypeResourceGroup {

		p.console.Message(ctx, fmt.Sprintf(
			"Parameter %s requires an %s resource group.", output.WithUnderline("%s", key), output.WithBold("existing")))
		rgName, err := p.prompters.PromptResourceGroup(ctx, prompt.PromptResourceOptions{
			DisableCreateNew: true,
		})
		if err != nil {
			return nil, err
		}
		value = rgName
	} else if paramType == provisioning.ParameterTypeString &&
		azdMetadata.Type != nil &&
		*azdMetadata.Type == azure.AzdMetadataTypeGenerateOrManual {

		var manualUserInput bool
		defaultOption := "Auto generate"
		options := []string{defaultOption, "Manual input"}
		choice, err := p.console.Select(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf(
				"Parameter %s can be either autogenerated or you can enter its value. What would you like to do?", key),
			Options:      options,
			DefaultValue: defaultOption,
		})
		if err != nil {
			return nil, err
		}
		manualUserInput = options[choice] != defaultOption

		if manualUserInput {
			resultValue, err := promptWithValidation(ctx, p.console, input.ConsoleOptions{
				Message:    msg,
				Help:       help,
				IsPassword: isSecuredParam,
			}, convertString, validateLengthRange(key, param.MinLength, param.MaxLength))
			if err != nil {
				return nil, err
			}
			value = resultValue
		} else {
			genValue, err := autoGenerate(key, azdMetadata)
			if err != nil {
				return nil, err
			}
			value = genValue
		}
	} else if param.AllowedValues != nil {
		options := make([]string, 0, len(*param.AllowedValues))
		for _, option := range *param.AllowedValues {
			options = append(options, fmt.Sprintf("%v", option))
		}

		if len(options) == 0 {
			return nil, fmt.Errorf("parameter '%s' has no allowed values defined", key)
		}

		// defaultOption enables running with --no-prompt, taking the default value. We use the first option as default.
		defaultOption := options[0]
		// user can override the default value with azd metadata
		if azdMetadata.Default != nil {
			defaultValStr := fmt.Sprintf("%v", azdMetadata.Default)
			if !slices.Contains(options, defaultValStr) {
				return nil, fmt.Errorf(
					"default value '%s' is not in the allowed values for parameter '%s'", defaultValStr, key)
			}
			defaultOption = defaultValStr
		}

		choice, err := p.console.Select(ctx, input.ConsoleOptions{
			Message:      msg,
			Help:         help,
			Options:      options,
			DefaultValue: defaultOption,
		})
		if err != nil {
			return nil, err
		}
		value = (*param.AllowedValues)[choice]
	} else {
		var defaultValueForPrompt any
		if azdMetadata.Default != nil {
			defaultValueForPrompt = azdMetadata.Default
		}
		switch paramType {
		case provisioning.ParameterTypeBoolean:
			options := []string{"False", "True"}
			if defaultValueForPrompt != nil {
				strVal := fmt.Sprintf("%v", defaultValueForPrompt)
				if strings.ToLower(strVal) == "true" {
					defaultValueForPrompt = "True"
				} else {
					defaultValueForPrompt = "False"
				}
			}
			choice, err := p.console.Select(ctx, input.ConsoleOptions{
				Message:      msg,
				Help:         help,
				Options:      options,
				DefaultValue: defaultValueForPrompt,
			})
			if err != nil {
				return nil, err
			}
			value = (options[choice] == "True")
		case provisioning.ParameterTypeNumber:
			if defaultValueForPrompt != nil {
				switch v := defaultValueForPrompt.(type) {
				case int:
					defaultValueForPrompt = fmt.Sprintf("%d", v)
				case float64:
					defaultValueForPrompt = fmt.Sprintf("%d", int(v))
				default:
					return nil, fmt.Errorf("unsupported default value type %T for number parameter: %v", v, key)
				}
			}
			userValue, err := promptWithValidation(ctx, p.console, input.ConsoleOptions{
				Message:      msg,
				Help:         help,
				DefaultValue: defaultValueForPrompt,
			}, convertInt, validateValueRange(key, param.MinValue, param.MaxValue))
			if err != nil {
				return nil, err
			}
			value = userValue
		case provisioning.ParameterTypeString:
			userValue, err := promptWithValidation(ctx, p.console, input.ConsoleOptions{
				Message:      msg,
				Help:         help,
				IsPassword:   isSecuredParam,
				DefaultValue: defaultValueForPrompt,
			}, convertString, validateLengthRange(key, param.MinLength, param.MaxLength))
			if err != nil {
				return nil, err
			}
			value = userValue
		case provisioning.ParameterTypeArray:
			userValue, err := promptWithValidation(ctx, p.console, input.ConsoleOptions{
				Message: msg,
				Help:    help,
			}, convertJson[[]any], validateJsonArray)
			if err != nil {
				return nil, err
			}
			value = userValue
		case provisioning.ParameterTypeObject:
			userValue, err := promptWithValidation(ctx, p.console, input.ConsoleOptions{
				Message: msg,
				Help:    help,
			}, convertJson[map[string]any], validateJsonObject)
			if err != nil {
				return nil, err
			}
			value = userValue
		default:
			panic(fmt.Sprintf("unknown parameter type: %s", provisioning.ParameterTypeFromArmType(param.Type)))
		}
	}

	return value, nil
}

// promptWithValidation prompts for a value using the console and then validates that it satisfies all the validation
// functions. If it does, it is converted from a string to a value using the converter and returned. If any validation
// fails, the prompt is retried after printing the error (prefixed with "Error: ") to the console. If there are is an
// error prompting it is returned as is.
func promptWithValidation[T any](
	ctx context.Context,
	console input.Console,
	options input.ConsoleOptions,
	converter func(string) T,
	validators ...func(string) error,
) (T, error) {
	for {
		userValue, err := console.Prompt(ctx, options)
		if err != nil {
			return *new(T), err
		}

		isValid := true

		for _, validator := range validators {
			if err := validator(userValue); err != nil {
				console.Message(ctx, output.WithErrorFormat("Error: %s.", err))
				isValid = false
				break
			}
		}

		if isValid {
			return converter(userValue), nil
		}
	}
}

func convertString(s string) string {
	return s
}

func convertInt(s string) int {
	if i, err := strconv.ParseInt(s, 10, 64); err != nil {
		panic(fmt.Sprintf("convertInt: %v", err))
	} else {
		return int(i)
	}
}

func convertJson[T any](s string) T {
	var t T
	if err := json.Unmarshal([]byte(s), &t); err != nil {
		panic(fmt.Sprintf("convertJson: %v", err))
	}
	return t
}
