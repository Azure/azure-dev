// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"os"

	"github.com/spf13/pflag"
)

// EnvFlag is a flag that represents the environment name and type. Actions which inject an environment
// should also use this flag so the user can control what environment is loaded in a uniform way across all our commands.
type EnvFlag struct {
	EnvironmentName     string
	EnvironmentType     string
	fromEnvVarValue     string
	fromEnvTypeVarValue string
}

// EnvironmentNameFlagName is the full name of the flag as it appears on the command line.
const EnvironmentNameFlagName string = "environment"

// EnvironmentTypeFlagName is the full name of the environment type flag as it appears on the command line.
const EnvironmentTypeFlagName string = "env-type"

// envNameEnvVarName is the same as environment.EnvNameEnvVarName, but duplicated here to prevent an import cycle.
const envNameEnvVarName = "AZURE_ENV_NAME"

// envTypeEnvVarName is the environment variable name for environment type.
const envTypeEnvVarName = "AZURE_ENV_TYPE"

func (e *EnvFlag) Bind(local *pflag.FlagSet, global *GlobalCommandOptions) {
	e.fromEnvVarValue = os.Getenv(envNameEnvVarName)
	e.fromEnvTypeVarValue = os.Getenv(envTypeEnvVarName)

	local.StringVarP(
		&e.EnvironmentName,
		EnvironmentNameFlagName,
		"e",
		// Set the default value to AZURE_ENV_NAME value if available
		e.fromEnvVarValue,
		"The name of the environment to use.")

	local.StringVar(
		&e.EnvironmentType,
		EnvironmentTypeFlagName,
		// Set the default value to AZURE_ENV_TYPE value if available
		e.fromEnvTypeVarValue,
		"The type of the environment (e.g., dev, test, prod). (Experimental)")
}

// checks if the environment name was set from the command line
func (e *EnvFlag) FromArg() bool {
	return e.EnvironmentName != "" && e.EnvironmentName != e.fromEnvVarValue
}

// checks if the environment type was set from the command line or environment variable
func (e *EnvFlag) TypeFromArg() bool {
	return e.EnvironmentType != "" && e.EnvironmentType != e.fromEnvTypeVarValue
}
