// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `$ref` decodes rather than being refused, and survives being written back.
//
// It was a directive the strict decoder rejected, on the reasoning that one
// reaching the decoder meant resolution had been skipped. That reasoning only
// held while every reader resolved. The commands that read, modify and save the
// file deliberately do not, because saving a resolved configuration inlines the
// author's includes -- so the decoder has to carry the directive through
// untouched instead of naming it a typo.
//
// TestRefResolvesOnTheCLIPathToo covers the resolved route;
// TestEditingReadsLeaveIncludesAlone covers the round trip.
func TestARefDirectiveDecodesAndSurvives(t *testing.T) {
	withRef := []byte(`
evaluators:
  - $ref: ./evaluators/quality.yaml
evals:
  - name: nightly
`)

	cfg, err := DecodeEvalConfig(withRef, "azure.eval.yaml")

	require.NoError(t, err, "the editing readers hand this straight to the decoder")
	require.Len(t, cfg.Evaluators, 1)
	assert.Equal(t, "./evaluators/quality.yaml", cfg.Evaluators[0].Ref)
	assert.Empty(t, cfg.Evaluators[0].Name,
		"an entry that is only a $ref has no name until the file it names supplies one")

	// The spelling that needs no resolution at all.
	cfg, err = DecodeEvalConfig([]byte(`
evaluators:
  - name: quality
    source: ./evaluators/quality.json
evals:
  - name: nightly
`), "azure.eval.yaml")

	require.NoError(t, err)
	assert.Equal(t, "./evaluators/quality.json", cfg.Evaluators[0].Source)
	assert.Empty(t, cfg.Evaluators[0].Ref)
}
