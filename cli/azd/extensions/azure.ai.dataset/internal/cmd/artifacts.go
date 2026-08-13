// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "azureaidataset/internal/messages"

// envKeyDatasetVersion records the version resolved at the last publish. The
// eval extension writes the same key; nothing reads it yet, so it exists for
// the user's own scripts and for `azd env get-values`.
const envKeyDatasetVersion = "EVAL_DATASET_VERSION"

// checkAssetExistence enforces the one difference between create and update.
func checkAssetExistence(verb, kind, name string, exists bool) error {
	switch {
	case verb == "create" && exists:
		return messages.AssetAlreadyExists(kind, name)
	case verb == "update" && !exists:
		return messages.AssetDoesNotExist(kind, name)
	}
	return nil
}
