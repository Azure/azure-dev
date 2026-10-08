// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/dataset_api"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An empty listing says which listing was empty.
//
// The caller already passed the sentence to print, and this printed a different
// one: `dataset versions list <name>` answered "No datasets found", a report
// about the whole project, for a name that simply had no versions. The
// parameter was threaded through and then ignored, so the fix that added it
// changed nothing and no test noticed.
func TestEmptyDatasetListingUsesTheCallersMessage(t *testing.T) {
	cases := []struct {
		name      string
		whenEmpty string
	}{
		{"whole project", messages.NoDatasets()},
		{"one dataset's versions", messages.NoDatasetVersions("golden")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&out)

			require.NoError(t, renderDatasets(cmd, &dataset_api.DatasetList{}, tc.whenEmpty))
			assert.Equal(t, tc.whenEmpty, out.String())
		})
	}
}
