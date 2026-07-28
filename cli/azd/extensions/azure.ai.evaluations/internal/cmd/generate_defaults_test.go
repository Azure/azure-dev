// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// --dataset is documented as taking a path or the name of a registered
// dataset, and means "use this one instead of generating". Only a local path
// used to suppress generation, so passing a registered name still submitted a
// generation job.
func TestGenerateScaffoldSkipsDatasetWhenSupplied(t *testing.T) {
	cases := []struct {
		name        string
		datasetFlag string
		wantSpec    bool
	}{
		{"registered name", "prod-sample", false},
		{"relative path", "./data/golden.jsonl", false},
		{"bare filename", "golden.jsonl", false},
		{"not supplied", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A path that does not exist means flags alone drive the config.
			cfg, err := resolveGenerateConfig(
				filepath.Join(t.TempDir(), "absent.yaml"),
				"my-agent", "gpt-4.1-nano", tc.datasetFlag, 0, 0)
			require.NoError(t, err)

			if tc.wantSpec {
				require.NotNil(t, cfg.Generate.Dataset,
					"a dataset spec is needed when none was supplied")
			} else {
				require.Nil(t, cfg.Generate.Dataset,
					"a supplied dataset must not produce a generation spec")
			}
			require.NotNil(t, cfg.Generate.Rubric, "the rubric spec is independent")
		})
	}
}
