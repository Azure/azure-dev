// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package curestclient_test

import (
	"strings"
	"testing"

	restclient "azure.ai.content-understanding/cu-rest-client"
	"github.com/stretchr/testify/assert"
)

func TestDiagnosticRedactionRetainsReasonsAndModelNames(t *testing.T) {
	for _, sample := range []struct {
		message, expected string
	}{
		{`Provided model 'gpt-6-luna' is unsupported.`, "model gpt-6-luna is unsupported."},
		{`Provided model "gpt-6-luna" is unsupported.`, "model gpt-6-luna is unsupported."},
		{"Analyzer's configuration conflicts with the resource's mapping.", "resource's mapping."},
		{"The processing region is unavailable.", "processing region is unavailable."},
		{"Invalid value 'private-document'.", "Invalid value [redacted value]."},
		{"Invalid value 'private-document", "Invalid value [redacted value]"},
		{"Read https://user:private-password@example.test/private-file?sig=private-signature", "[redacted URL]"},
		{"Authorization: Bearer private-token", "Authorization: [redacted]"},
		{"api_key=private-key", "[redacted value]"},
		{"content: private-document", "[redacted value]"},
		{"path: /private-document.pdf", "[redacted value]"},
		{"File C:\\private-folder\\private-file.pdf is unreadable.", "[redacted path]"},
		{"Bad fieldSchema.fields.private-key.type", "fieldSchema.fields.[redacted]"},
		{"Bad\x1b[31m\u202e document.", "document."},
	} {
		t.Run(sample.message, func(t *testing.T) {
			safe := restclient.RedactDiagnostic(sample.message)
			assert.Contains(t, safe, sample.expected)
			assert.NotContains(t, safe, "private")
			assert.NotContains(t, safe, "\x1b")
			assert.NotContains(t, safe, "\u202e")
			assert.Equal(t, safe, restclient.RedactDiagnostic(safe), "repeated boundary filtering is stable")
		})
	}
	for _, value := range []string{string([]byte{0xff}), strings.Repeat("x", 3000)} {
		safe := restclient.RedactDiagnostic(value)
		assert.Contains(t, safe, "Diagnostic omitted")
		assert.LessOrEqual(t, len(safe), restclient.MaxDiagnosticBytes)
	}
}
