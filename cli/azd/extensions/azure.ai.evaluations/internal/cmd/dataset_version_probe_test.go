// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/dataset_api"

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

// The probe still cannot prove absence -- it only ever proves existence, and a
// dataset whose early versions were deleted has none of them left to find. So
// an absence the service never confirmed has to publish rather than refuse,
// otherwise the caller is sent to `create`, which fails in turn once the
// listing catches up and reports the name already taken.
func TestCheckAssetExistenceLetsAnUnprovableAbsenceThrough(t *testing.T) {
	assert.NoError(t, checkAssetExistence("update", "dataset", "x", false, false))

	err := checkAssetExistence("update", "dataset", "x", false, true)
	assert.Error(t, err, "a 404 is the service saying the name is unknown, which still refuses")
}
