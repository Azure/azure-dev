// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package internal

import (
	"os"
	"strings"
	"unicode"
)

func ToPtr[T any](value T) *T {
	return &value
}

func ToPascalCase(value string) string {
	parts := strings.Split(value, ".")

	for i, part := range parts {
		if len(part) > 0 {
			runes := []rune(part)
			runes[0] = unicode.ToUpper(runes[0])
			parts[i] = string(runes)
		}
	}

	return strings.Join(parts, ".")
}

const (
	PermissionDirectory      os.FileMode = 0755
	PermissionExecutableFile os.FileMode = 0755
	PermissionFile           os.FileMode = 0644

	PermissionDirectoryOwnerOnly os.FileMode = 0700
	PermissionFileOwnerOnly      os.FileMode = 0600

	PermissionMaskDirectoryExecute os.FileMode = 0100
)
