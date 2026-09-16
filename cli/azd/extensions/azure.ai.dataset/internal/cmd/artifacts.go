// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "azureaidataset/internal/messages"

// envKeyDatasetVersion records the version resolved at the last publish, for
// the author's own scripts and for `azd env get-values`. Nothing reads it here.
//
// One key for every dataset, so publishing a second one replaces the first
// one's value and the key does not say which dataset it belongs to. That is
// only useful to a project publishing a single dataset, which is why it is a
// convenience rather than something to count on.
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
