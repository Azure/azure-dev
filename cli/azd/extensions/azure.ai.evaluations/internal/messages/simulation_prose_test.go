// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
)

func TestSimulationModelProseEscapesUntrustedReferences(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if !unicode.IsControl(r) && (!unicode.IsSpace(r) || r == ' ') {
			continue
		}
		t.Run(fmt.Sprintf("U%04X", r), func(t *testing.T) {
			model := "model" + string(r) + "spoofed"
			for _, output := range []string{
				AmbiguousSimulationModel([]string{model, "other/model"}).Error(),
				DetectedSimulationModel(model), UnverifiedSimulationModel(model),
			} {
				assert.NotContains(t, output, model)
				assert.Contains(t, output, fmt.Sprintf("%q", model))
				assert.NotContains(t, strings.TrimSuffix(output, "\n"), string(r))
			}
		})
	}
	for _, model := range []string{"gpt-4o-mini", "connection/mod\u00e8le"} {
		assert.Contains(t, DetectedSimulationModel(model), model)
		assert.Contains(t, UnverifiedSimulationModel(model), model)
	}
}
