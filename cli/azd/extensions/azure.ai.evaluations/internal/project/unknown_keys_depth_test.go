// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A typo is named wherever it appears, not only at the top level.
//
// yaml.Node.Decode does not inherit KnownFields from the decoder that reached
// it, so a misspelt key inside an evaluator entry was dropped in silence while
// the same misspelling one level up was reported. A pinned `verison` that does
// nothing is worse than one that is refused: the run grades against whatever
// version happens to be latest and reports success.
func TestUnknownKeysAreNamedAtEveryDepth(t *testing.T) {
	cases := []struct {
		where  string
		body   string
		key    string
		nearer string
		line   string
	}{
		{
			where:  "top level of an eval",
			body:   "evals:\n  - name: e1\n    datasett: golden\n",
			key:    "datasett",
			nearer: "dataset",
			line:   "line 3",
		},
		{
			where:  "inside an evaluator entry",
			body:   "evals:\n  - name: e1\n    evaluators:\n      - evaluator: builtin.x\n        verison: \"3\"\n",
			key:    "verison",
			nearer: "version",
			line:   "line 5",
		},
		{
			where:  "inside a dataset declaration",
			body:   "datasets:\n  - name: golden\n    fil: ./rows.jsonl\n",
			key:    "fil",
			nearer: "file",
			line:   "line 3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.where, func(t *testing.T) {
			_, err := DecodeEvalConfig([]byte(tc.body), "azure.eval.yaml")
			require.Errorf(t, err, "%q was accepted in silence", tc.key)

			assert.Contains(t, err.Error(), tc.key, "the message has to name the key")
			assert.Contains(t, err.Error(), tc.nearer, "and the key it was probably meant to be")
			assert.Contains(t, err.Error(), tc.line,
				"pointing at the line in the file, not inside an extracted fragment")
		})
	}
}
