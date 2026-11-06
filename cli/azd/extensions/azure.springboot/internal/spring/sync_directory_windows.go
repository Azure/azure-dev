// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package spring

import "os"

func syncCredentialRepairDirectory(*os.Root, string) error {
	// Windows file replacement flushes directory metadata through the filesystem.
	return nil
}
