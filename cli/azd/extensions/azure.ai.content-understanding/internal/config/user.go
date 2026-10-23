// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"context"
	"encoding/json"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// UserConfigPath is the CU namespace in the host's azd user configuration.
const UserConfigPath = "extensions.ai-content-understanding"

// SettingNames lists the canonical CU settings supported by user defaults.
var SettingNames = []string{"CU_ENDPOINT", "CU_DEFAULT_ANALYZER", "CU_API_VERSION"}

// ValidateSettingName rejects unsupported names without echoing the input.
func ValidateSettingName(name string) error {
	switch name {
	case "CU_ENDPOINT", "CU_DEFAULT_ANALYZER", "CU_API_VERSION":
		return nil
	default:
		return invalid("unsupported_setting",
			"Supported setting names are CU_ENDPOINT, CU_DEFAULT_ANALYZER, and CU_API_VERSION (case-sensitive).")
	}
}

// ValidateSetting applies the same validation to stored defaults and effective settings.
func ValidateSetting(name, value string) error {
	if err := ValidateSettingName(name); err != nil {
		return err
	}
	if value == "" {
		return invalid("empty_setting", "CU settings must not be empty. Supply a value or unset the setting.")
	}
	switch name {
	case "CU_ENDPOINT":
		_, err := cuclient.NormalizeEndpoint(value)
		return err
	case "CU_DEFAULT_ANALYZER":
		return cuclient.ValidateAnalyzerID(value)
	default:
		_, err := cuclient.NormalizeAPIVersion(value)
		return err
	}
}

// ReadUserDefaults reads the CU namespace without requiring project or authentication context.
func ReadUserDefaults(ctx context.Context, client *azdext.AzdClient) (map[string]json.RawMessage, error) {
	response, err := client.UserConfig().Get(ctx, &azdext.GetUserConfigRequest{Path: UserConfigPath})
	if err != nil {
		return nil, exterrors.FromError(err)
	}
	if response == nil {
		return nil, invalid("invalid_user_config", "The azd host returned no user configuration response.")
	}
	if !response.GetFound() {
		return map[string]json.RawMessage{}, nil
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(response.GetValue(), &settings); err != nil || settings == nil {
		return nil, invalid("invalid_user_config",
			"azd user configuration extensions.ai-content-understanding must be a JSON object.")
	}
	return settings, nil
}

// UserSetting validates a stored value only when the caller selects it.
func UserSetting(settings map[string]json.RawMessage, name string) (string, bool, error) {
	raw, found := settings[name]
	if !found {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, invalid("invalid_user_config", "CU user defaults must be nonempty strings.")
	}
	if err := ValidateSetting(name, value); err != nil {
		return "", true, err
	}
	return value, true, nil
}
