// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows && !linux && !darwin

package osversion

import "errors"

func GetVersion() (string, error) {
	return "", errors.New("unsupported OS")
}
