// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resource

import (
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
)

func getExecutionEnvironment() string {
	// calling programs receive the highest priority, since they end up wrapping the CLI and are the most
	// inner layers.
	env := execEnvFromCaller()

	if env == "" {
		// machine-level CI execution environments
		env = execEnvForCi()
	}

	// no execution environment found, default to plain desktop
	if env == "" {
		env = fields.EnvDesktop
	}

	// global modifiers that are applicable to all environments
	modifiers := execEnvModifiers()

	return strings.Join(append([]string{env}, modifiers...), ";")
}

func execEnvFromCaller() string {
	userAgent := internal.GetCallerUserAgent()

	if strings.Contains(userAgent, internal.VsCodeAgentPrefix) {
		return fields.EnvVisualStudioCode
	}

	if _, ok := os.LookupEnv("AZD_IN_CLOUDSHELL"); ok {
		return fields.EnvCloudShell
	}

	return ""
}

func execEnvModifiers() []string {
	modifiers := []string{}
	userAgent := internal.GetCallerUserAgent()

	if strings.Contains(userAgent, "azure_app_space_portal") {
		modifiers = append(modifiers, fields.EnvModifierAzureSpace)
	}

	return modifiers
}
