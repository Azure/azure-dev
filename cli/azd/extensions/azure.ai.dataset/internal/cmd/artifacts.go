// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "fmt"

// envKeyDatasetVersion caches the version resolved at the last publish, so a
// later read does not have to list every version to find the newest.
const envKeyDatasetVersion = "EVAL_DATASET_VERSION"

// checkAssetExistence enforces the one difference between create and update.
func checkAssetExistence(verb, kind, name string, exists bool) error {
	switch {
	case verb == "create" && exists:
		return fmt.Errorf(
			"%s %q already exists: use `update` to publish a new version", kind, name)
	case verb == "update" && !exists:
		return fmt.Errorf(
			"%s %q does not exist: use `create` to register it", kind, name)
	}
	return nil
}
