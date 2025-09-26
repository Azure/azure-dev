// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"os"

	"github.com/spf13/pflag"
)

// EnvFlag is a flag that represents the environment name. Actions which inject an environment should also use this flag
// so the user can control what environment is loaded in a uniform way across all our commands.
type EnvFlag struct {
	EnvironmentName string
	fromEnvVarValue string
}

// EnvironmentNameFlagName is the full name of the flag as it appears on the command line.
const EnvironmentNameFlagName string = "environment"

// envNameEnvVarName is the same as environment.EnvNameEnvVarName, but duplicated here to prevent an import cycle.
const envNameEnvVarName = "AZURE_ENV_NAME"

func (e *EnvFlag) Bind(local *pflag.FlagSet, global *GlobalCommandOptions) {
	e.fromEnvVarValue = os.Getenv(envNameEnvVarName)
	local.StringVarP(
		&e.EnvironmentName,
		EnvironmentNameFlagName,
		"e",
		// Set the default value to AZURE_ENV_NAME value if available
		e.fromEnvVarValue,
		"The name of the environment to use.")
}

// checks if the environment name was set from the command line
func (e *EnvFlag) FromArg() bool {
	return e.EnvironmentName != "" && e.EnvironmentName != e.fromEnvVarValue
}
