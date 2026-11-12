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

func TestDiagnosticRedactionNormalizesBeforeMatching(t *testing.T) {
	for _, message := range []string{
		"payload: prefix\r\x00DEMO_VALUE",
		"payload:\n\x00DEMO_VALUE",
		"payload:\r\n\x00DEMO_VALUE",
		"payload:\t\x00DEMO_VALUE",
		"pay\x00load: DEMO_VALUE",
		"pay\u202eload: DEMO_VALUE",
		"payload\x00:\tDEMO_VALUE",
		"Invalid value '\r\x00DEMO_VALUE'.",
		"Read htt\x00ps://example.test/DEMO_VALUE",
		"Bad fieldSchema.fie\x00lds.DEMO_VALUE.type",
	} {
		t.Run(message, func(t *testing.T) {
			safe := restclient.RedactDiagnostic(message)
			assert.NotContains(t, safe, "DEMO_VALUE")
			assert.Contains(t, safe, "[redacted")
			assert.NotContains(t, safe, "\x00")
			assert.NotContains(t, safe, "\r")
			assert.NotContains(t, safe, "\u202e")
			assert.Equal(t, safe, restclient.RedactDiagnostic(safe))
		})
	}
	assert.Equal(t, "First reason.\n\tSecond reason.",
		restclient.RedactDiagnostic("First reason.\r\n\tSecond reason."))
	assert.Equal(t, "Provided model gpt-6-luna is unsupported.",
		restclient.RedactDiagnostic("Provided model 'gpt-\x006-luna' is unsupported."))
}
