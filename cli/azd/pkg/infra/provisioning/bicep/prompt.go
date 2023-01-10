package bicep

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"golang.org/x/exp/slices"

	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

func (p *BicepProvider) promptForParameter(
	ctx context.Context,
	key string,
	param azure.ArmTemplateParameterDefinition,
) (any, error) {
	msg := fmt.Sprintf("Please enter a value for the '%s' infrastructure parameter:", key)
	help, _ := param.Description()
	azdMetadata, _ := param.AzdMetadata()
	paramType := p.mapBicepTypeToInterfaceType(param.Type)

	var value any

	if paramType == ParameterTypeString && azdMetadata.Type != nil && *azdMetadata.Type == "location" {
		location, err := azureutil.PromptLocationWithFilter(
			ctx, p.env, msg, help, p.console, p.azCli, func(loc azcli.AzCliLocation) bool {
				if param.AllowedValues == nil {
					return true
				}

				return slices.IndexFunc(*param.AllowedValues, func(v any) bool {
					s, ok := v.(string)
					return ok && loc.Name == s
				}) != -1
			},
		)
		if err != nil {
			return nil, err
		}
		value = location
	} else if param.AllowedValues != nil {
		options := make([]string, 0, len(*param.AllowedValues))
		for _, option := range *param.AllowedValues {
			options = append(options, fmt.Sprintf("%v", option))
		}
		choice, err := p.console.Select(ctx, input.ConsoleOptions{
			Message: msg,
			Help:    help,
			Options: options,
		})
		if err != nil {
			return nil, err
		}
		value = (*param.AllowedValues)[choice]
	} else {
		switch paramType {
		case ParameterTypeBoolean:
			options := []string{"False", "True"}
			choice, err := p.console.Select(ctx, input.ConsoleOptions{
				Message: msg,
				Help:    help,
				Options: options,
			})
			if err != nil {
				return nil, err
			}
			value = (options[choice] == "True")
		case ParameterTypeNumber:
			userValue, err := promptWithValidation(ctx, p.console, input.ConsoleOptions{
				Message: msg,
				Help:    help,
			}, convertInt, validateValueRange(key, param.MinValue, param.MaxValue))
			if err != nil {
				return nil, err
			}
			value = userValue
		case ParameterTypeString:
			userValue, err := promptWithValidation(ctx, p.console, input.ConsoleOptions{
				Message: msg,
				Help:    help,
			}, convertString, validateLengthRange(key, param.MinLength, param.MaxLength))
			if err != nil {
				return nil, err
			}
			value = userValue
		case ParameterTypeArray:
			userValue, err := promptWithValidation(ctx, p.console, input.ConsoleOptions{
				Message: msg,
				Help:    help,
			}, convertJson[[]any], validateJsonArray)
			if err != nil {
				return nil, err
			}
			value = userValue
		case ParameterTypeObject:
			userValue, err := promptWithValidation(ctx, p.console, input.ConsoleOptions{
				Message: msg,
				Help:    help,
			}, convertJson[map[string]any], validateJsonObject)
			if err != nil {
				return nil, err
			}
			value = userValue
		default:
			panic(fmt.Sprintf("unknown parameter type: %s", p.mapBicepTypeToInterfaceType(param.Type)))
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
