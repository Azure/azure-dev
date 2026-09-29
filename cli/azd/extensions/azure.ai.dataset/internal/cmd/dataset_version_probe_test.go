// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaidataset/internal/pkg/dataset_api"

	"github.com/stretchr/testify/assert"
)

// The existence probe exists for one case: `create` then immediately `update`,
// where the version listing has not caught up. It probed a hardcoded "1" while
// this CLI's first publish is NextVersion(""), which is "1.0" -- so for the
// case it was written for it read a version that never existed and the fallback
// was inert. Deriving it keeps the two in step if the base ever moves.
func TestFirstDatasetVersionsCoverWhatThisCLIPublishes(t *testing.T) {
	assert.Contains(t, firstDatasetVersions, dataset_api.NextVersion(""),
		"the probe has to look for the version a create actually writes")
	assert.Contains(t, firstDatasetVersions, "1",
		"a generation job, the SDK or the portal can register a plain 1")
}
