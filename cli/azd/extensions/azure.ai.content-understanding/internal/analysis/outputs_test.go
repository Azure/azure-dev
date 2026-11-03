// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package analysis

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/require"
)

func TestOutputCollisionFallbacksAndStableMapping(t *testing.T) {
	digest := sha256.Sum256([]byte("a"))
	hash := hex.EncodeToString(digest[:])
	for _, llm := range []bool{false, true} {
		for reserved := 0; reserved <= 8; reserved++ {
			t.Run(fmt.Sprintf("llm=%t/reserved=%d", llm, reserved), func(t *testing.T) {
				directory := testutil.TempDir(t)
				inputs := []Input{
					{Path: "a", Reference: "a", Relative: "document.pdf"},
					{Path: "b", Reference: "b", Relative: "document.pdf"},
				}
				for index := range reserved {
					name := fmt.Sprintf("reserved-%d", index)
					inputs = append(inputs, Input{
						Path: name, Reference: name, Relative: "document.pdf." + hash[:8*(index+1)],
					})
				}
				options := OutputOptions{Existing: "error", Directory: directory, LLMInput: llm}
				plan, err := PlanOutputs(inputs, options)
				if reserved == 8 {
					requireCode(t, err, "output_collision")
					require.Empty(t, plan.Outputs)
				} else {
					require.NoError(t, err)
					suffix := ".result.json"
					if llm {
						suffix = ".result.md"
					}
					require.Equal(t, filepath.Join(directory, "document.pdf."+hash[:8*(reserved+1)]+suffix),
						*plan.Outputs[0].Path)
					mapping := func(plan Plan) map[string]string {
						result := map[string]string{}
						for _, output := range plan.Outputs {
							require.NotNil(t, output.Path)
							require.False(t, output.Exists)
							require.False(t, output.Skipped)
							result[output.Input.Reference] = *output.Path
						}
						return result
					}
					reversed := slices.Clone(inputs)
					slices.Reverse(reversed)
					other, err := PlanOutputs(reversed, options)
					require.NoError(t, err)
					require.Equal(t, mapping(plan), mapping(other))
					preview := DryRun{DryRun: true, Analyzer: "prebuilt-invoice"}
					for _, output := range plan.Outputs {
						preview.Outputs = append(preview.Outputs, DryRunOutput{
							Input: output.Input.Reference, Output: output.Path,
						})
					}
					require.Equal(t, preview, plan.Preview(preview.Analyzer))
				}
				entries, err := os.ReadDir(directory)
				require.NoError(t, err)
				require.Empty(t, entries, "planning and preview must not publish files")
			})
		}
	}
}

func TestOutputCollisionNamesRespectUTF8ByteBudget(t *testing.T) {
	for _, llm := range []bool{false, true} {
		t.Run(fmt.Sprintf("llm=%t", llm), func(t *testing.T) {
			inputs := []Input{
				{Path: "a", Reference: "a", Relative: strings.Repeat("界", 80)},
				{Path: "b", Reference: "b", Relative: strings.Repeat("界", 80)},
			}
			plan, err := PlanOutputs(inputs, OutputOptions{Existing: "error", Directory: testutil.TempDir(t), LLMInput: llm})
			require.NoError(t, err)
			suffix := ".result.json"
			if llm {
				suffix = ".result.md"
			}
			for _, output := range plan.Outputs {
				digest := sha256.Sum256([]byte(output.Input.Reference))
				tail := "." + hex.EncodeToString(digest[:])[:8] + suffix
				name := filepath.Base(*output.Path)
				require.True(t, utf8.ValidString(name))
				require.Equal(t, strings.Repeat("界", (240-len(tail))/3)+tail, name)
				require.LessOrEqual(t, len(name), 240)
			}
		})
	}
}

func TestRemoteOutputNameByteBoundaryAndExplicitFile(t *testing.T) {
	for _, llm := range []bool{false, true} {
		t.Run(fmt.Sprintf("llm=%t", llm), func(t *testing.T) {
			suffix := ".result.json"
			if llm {
				suffix = ".result.md"
			}
			directory := testutil.TempDir(t)
			input := Input{URL: "https://example.com/document", Reference: "https://example.com/document"}
			budget := 240 - len(suffix)
			input.Relative = strings.Repeat("界", budget/3) + strings.Repeat("a", budget%3)
			options := OutputOptions{Existing: "error", Directory: directory, LLMInput: llm}
			plan, err := PlanOutputs([]Input{input}, options)
			require.NoError(t, err)
			require.Len(t, filepath.Base(*plan.Outputs[0].Path), 240)
			input.Relative += "a"
			_, err = PlanOutputs([]Input{input}, options)
			requireCode(t, err, "output_name_too_long")
			options.Directory = ""
			options.File = filepath.Join(directory, "explicit")
			plan, err = PlanOutputs([]Input{input}, options)
			require.NoError(t, err)
			require.Equal(t, options.File, *plan.Outputs[0].Path)
		})
	}
}
