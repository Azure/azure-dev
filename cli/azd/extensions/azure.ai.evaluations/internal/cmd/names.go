// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"regexp"
	"strings"
)

// assetNamePattern is what the service accepts for a dataset name. Its own
// refusal is a 400 carrying four levels of nested JSON, and the sentence that
// matters is at the bottom of it.
//
// This extension carries its own copy of the dataset commands, so it needs its
// own copy of the guard: without it the same mistyped name is refused clearly
// by `azd ai dataset` and obscurely by `azd ai eval dataset`.
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

// validLookupName reports whether a name can be used to address something that
// already exists.
//
// Deliberately looser than validAssetName. That one models the service's
// character set so a typo on create is reported here instead of coming back as
// a 400 wrapped in JSON -- which is right for creating, and wrong for looking
// up, because `generate` publishes names it does not apply. `generate
// --dataset-name "my data"` succeeded and `dataset versions list "my data"`
// then refused to name it: the extension could publish a dataset it could not
// read back.
//
// What stays refused is what cannot address anything, or what would leave the
// path. The name is URL-escaped into the request path, so a space is fine and a
// separator is not.
func validLookupName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > assetNameMaxLength {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
