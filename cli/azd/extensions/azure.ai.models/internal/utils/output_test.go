// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPrintObjectTo(t *testing.T) {
	rows := []struct {
		Name string `json:"name" table:"Name"`
	}{{Name: "test-model"}}
	for _, format := range []OutputFormat{FormatJSON, FormatTable} {
		t.Run(string(format), func(t *testing.T) {
			var out bytes.Buffer
			if err := PrintObjectTo(&out, rows, format); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "test-model") {
				t.Fatal("output did not reach the injected writer")
			}
			if format == FormatJSON && !json.Valid(out.Bytes()) {
				t.Fatal("JSON output contains non-JSON text")
			}
		})
	}
}
