// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package utils

import "strings"

func IsLocalFilePath(fileID string) bool {
	return strings.HasPrefix(fileID, "local:")
}

func GetLocalFilePath(fileID string) string {
	if IsLocalFilePath(fileID) {
		return fileID[6:]
	}
	return fileID
}
