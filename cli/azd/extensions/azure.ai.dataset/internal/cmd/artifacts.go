// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "azureaidataset/internal/messages"

// envKeyDatasetVersion records the version resolved at the last publish. The
// eval extension writes the same key; nothing reads it yet, so it exists for
// the user's own scripts and for `azd env get-values`.
const envKeyDatasetVersion = "EVAL_DATASET_VERSION"

// checkAssetExistence enforces the one difference between create and update.
//
// absenceCertain separates "the service says this name is unknown" from "nothing
// came back", and only the former refuses an update. The version listing is
// eventually consistent, so an update issued moments after a create reads an
// empty listing for a dataset that plainly exists. Refusing there strands the
// caller behind an error whose advice -- run `create` -- would fail too, because
// create sees the same listing catch up and reports the name already taken.
// Letting an unprovable absence through publishes a version, which is what was
// asked for either way: the upload does not care whether the name was new.
func checkAssetExistence(verb, kind, name string, exists, absenceCertain bool) error {
	switch {
	case verb == "create" && exists:
		return messages.AssetAlreadyExists(kind, name)
	case verb == "update" && !exists && absenceCertain:
		return messages.AssetDoesNotExist(kind, name)
	}
	return nil
}
