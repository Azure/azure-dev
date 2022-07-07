// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
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
