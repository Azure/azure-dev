// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "regexp"

// assetNamePattern is what the service accepts for a dataset name. Its own
// refusal is a 400 carrying four levels of nested JSON, and the sentence that
// matters is at the bottom of it.
var assetNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

const assetNameMaxLength = 255

// validAssetName reports whether the service will accept this name, so a
// mistyped one is refused before a round trip rather than after.
func validAssetName(name string) bool {
	if name == "" || len(name) > assetNameMaxLength {
		return false
	}
	return assetNamePattern.MatchString(name)
}
