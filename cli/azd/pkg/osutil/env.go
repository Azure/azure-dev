// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"fmt"
	"os"
	"runtime"
)

// GetenvOrDefault behaves like `os.Getenv`, except it returns
// a specified default value if the key is not present in the
// environment.
func GetenvOrDefault(key string, def string) string {
	if v, has := os.LookupEnv(key); has {
		return v
	}
	return def
}

func GetNewLineSeparator() string {
	if runtime.GOOS == "windows" {
		return "\r\n"
	} else {
		return "\n"
	}
}

const (
	emulatorEnvName string = "AZURE_AZ_EMULATOR"
)

// IsAzEmulator returns true if the AZURE_AZ_EMULATOR environment variable is defined.
// It does not matter the value of the environment variable, as long as it is defined.
func IsAzEmulator() bool {
	_, emulateEnvVarDefined := os.LookupEnv(emulatorEnvName)
	return emulateEnvVarDefined
}

func AzEmulateKey() string {
	return fmt.Sprintf("%s=%s", emulatorEnvName, "true")
}
