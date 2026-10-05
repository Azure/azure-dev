// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"unicode"

	"azureaieval/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupNameRejectsEveryUnicodeControl(t *testing.T) {
	for control := rune(0); control <= 0x9f; control++ {
		if !unicode.IsControl(control) {
			continue
		}
		t.Run(fmt.Sprintf("U+%04X", control), func(t *testing.T) {
			assert.False(t, validLookupName("before"+string(control)+"after"))
		})
	}
}

func TestLookupNamePreservesSafeUnicodeAndCreateRules(t *testing.T) {
	for _, name := range []string{"seed data", "caf\u00e9", "\u8a55\u4fa1", "quality-\U0001f9ea", ".seeds", "seeds.v2"} {
		t.Run(name, func(t *testing.T) {
			assert.True(t, validLookupName(name))
			assert.False(t, validAssetName(name), "lookup must remain distinct from the service create character set")
		})
	}
	for _, name := range []string{"seeds", "seeds_v2", "seeds-v2", "123"} {
		assert.True(t, validLookupName(name))
		assert.True(t, validAssetName(name))
	}
}

func TestInitDatasetRejectsC1FilenameBeforeWrites(t *testing.T) {
	for control := rune(0x80); control <= 0x9f; control++ {
		for _, output := range []string{"human", "json"} {
			t.Run(fmt.Sprintf("U+%04X/%s", control, output), func(t *testing.T) {
				h := newInitHarness(t, nil)
				filename := "before" + string(control) + "after.jsonl"
				require.NoError(t, os.WriteFile(filename, []byte(`{"query":"help","response":"hello"}`), 0o600))
				args := []string{"--name", "quality", "--dataset", "./" + filename,
					"--judge-model", "judge", "--conversation-mode", "static"}
				if output == "human" {
					args = append(args, "--no-prompt")
				} else {
					args = append(args, "--output", "json")
				}
				before := initFileSnapshot(t, h.dir)
				text, err := executeConversationInit(t, args...)
				require.ErrorContains(t, err, "invalid catalog name")
				validation, ok := errors.AsType[*azdext.LocalError](err)
				require.True(t, ok)
				assert.Equal(t, exterrors.CodeInvalidParameter, validation.Code)
				assert.Contains(t, validation.Suggestion, "Rename")
				assert.Contains(t, err.Error(), strconv.Quote("./"+filename))
				assert.NotContains(t, err.Error(), string(control))
				assert.Empty(t, text, "no prompts or success-shaped output")
				assert.Zero(t, h.project.wiringAttempts())
				assert.Empty(t, h.usage.reported())
				assert.Equal(t, before, initFileSnapshot(t, h.dir))
			})
		}
	}
}
