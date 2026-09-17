// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The service's own refusal is a 400 carrying four levels of nested JSON, and
// the sentence that matters sits at the bottom of it. Worse, the upload has
// already happened by then.
func TestValidAssetName(t *testing.T) {
	cases := []struct {
		name string
		want bool
		why  string
	}{
		{"support-golden", true, "dashes are allowed"},
		{"support_golden", true, "underscores are allowed"},
		{"golden123", true, "digits are allowed"},
		{"a", true, "one character is enough"},
		{"bugbash space 035200", false, "spaces are what a developer types first"},
		{"golden.jsonl", false, "dots read like a filename but are refused"},
		{"golden/v2", false, "a slash would change which resource is addressed"},
		{"golden%20", false, "an escape sequence typed by hand is not a name"},
		{"", false, "an empty name addresses the collection, not a dataset"},
		{"caf\u00e9-golden", false, "the service is alphanumeric ASCII only"},
		{strings.Repeat("a", 255), true, "255 is the documented limit"},
		{strings.Repeat("a", 256), false, "256 is over it"},
	}

	for _, c := range cases {
		assert.Equalf(t, c.want, validAssetName(c.name), "%s: %s", c.name, c.why)
	}
}
