// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"testing"
	"unicode"

	"github.com/stretchr/testify/require"
)

func TestSimulationRejectsUnicodeControlsAndWhitespace(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if !unicode.IsControl(r) && !unicode.IsSpace(r) {
			continue
		}
		for i, model := range []string{
			"model" + string(r), string(r) + "model", "mo" + string(r) + "del",
			"con" + string(r) + "nection/model", "connection/mo" + string(r) + "del",
		} {
			t.Run(fmt.Sprintf("U%04X/shape%d", r, i), func(t *testing.T) {
				require.Error(t, (&Simulation{Model: model}).Validate())
			})
		}
	}
}

func TestSimulationPreservesValidUnicodeReferences(t *testing.T) {
	for _, model := range []string{"gpt-4o-mini", "connection/gpt-4o-mini", "mod\u00e8le", "\u6a21\u578b",
		"connexion/mod\u00e8le", "\u63a5\u7d9a/\u6a21\u578b", "model-v1.2", "connection/model:v1"} {
		require.NoError(t, (&Simulation{Model: model, NumConversations: 3, MaxTurns: 7}).Validate())
	}
}
