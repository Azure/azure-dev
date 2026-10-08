// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `$ref` is answered from the document, and survives being written back.
//
// The commands that read, modify and save the file must not resolve, because
// saving a resolved configuration inlines the author's includes. They read the
// document rather than decoding it, so the directive is answered from there --
// and the strict decoder, which only ever sees a resolved configuration, is
// free to treat a surviving `$ref` as the bypass it is.
//
// TestRefResolvesOnTheCLIPathToo covers the resolved route;
// TestEditingReadsLeaveIncludesAlone covers the round trip.
func TestARefDirectiveIsVisibleToTheAuthoredRead(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, EvalConfigBase), []byte(`
evaluators:
  - $ref: ./evaluators/quality.yaml
evals:
  - name: nightly
`), 0o600))

	authored, err := ReadAuthoredConfig(dir)
	require.NoError(t, err, "the writing commands read the document, not a decoded config")
	assert.True(t, authored.HasUnnamedRef(SectionEvaluators),
		"an entry that is only a $ref has no name until the file it names supplies one")
	assert.Empty(t, authored.Names(SectionEvaluators))

	// The spelling that needs no resolution at all.
	cfg, err := DecodeEvalConfig([]byte(`
evaluators:
  - name: quality
    source: ./evaluators/quality.json
evals:
  - name: nightly
`), "azure.eval.yaml")

	require.NoError(t, err)
	assert.Equal(t, "./evaluators/quality.json", cfg.Evaluators[0].Source)
}

// A `$ref` that reaches the strict decoder means resolution was skipped, so it
// is reported rather than carried through.
func TestARefReachingTheStrictDecoderIsRefused(t *testing.T) {
	_, err := DecodeEvalConfig([]byte(`
evaluators:
  - $ref: ./evaluators/quality.yaml
evals:
  - name: nightly
`), "azure.eval.yaml")

	require.Error(t, err, "only a config that bypassed the resolver still carries the directive")
	assert.Contains(t, err.Error(), "$ref")
}
