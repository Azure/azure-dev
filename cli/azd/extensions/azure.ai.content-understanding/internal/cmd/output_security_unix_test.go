// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux || darwin

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azure.ai.content-understanding/internal/localio"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputCommandsRejectSharedDirectoriesBeforeServiceCalls(t *testing.T) {
	for _, operation := range []string{"analysis result", "analysis report", "schema", "analyzer test report"} {
		t.Run(operation, func(t *testing.T) {
			directory := testutil.TempDir(t)
			path := filepath.Join(directory, "result")
			_, checkErr := localio.Check(path)
			require.NoError(t, checkErr)
			require.NoError(t, os.Chmod(directory, 0o777)) // #nosec G302 -- Deliberately unsafe test directory.
			sample := schemaFile(t, "sample")
			fixture := newAnalyzeFixture(t)
			var err error
			switch operation {
			case "analysis result":
				_, _, err = fixture.execute(t.Context(), sample, "--output-file", path)
			case "analysis report":
				_, _, err = fixture.execute(t.Context(), sample, "--report-file", path)
			case "schema":
				_, _, err = executeSchemaCreate(fixture, t.Context(), "--output-file", path)
			case "analyzer test report":
				_, err = executeAnalyzerTest(fixture, t.Context(), "invoice", sample, "--output-file", path)
			}
			require.Error(t, err)
			assert.Zero(t, fixture.opens)
			assert.Zero(t, fixture.client.calls.Load())
			entries, err := os.ReadDir(directory)
			require.NoError(t, err)
			assert.Empty(t, entries)
		})
	}
}
