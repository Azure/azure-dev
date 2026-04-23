// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// displayWidth returns the number of runes (visual columns) in s.
// Box-drawing and block characters are each one column wide, but multi-byte
// in UTF-8, so we count runes instead of bytes.
func displayWidth(s string) int {
	return utf8.RuneCountInString(s)
}

func TestBannerFitsWithin100Columns(t *testing.T) {
	for i, line := range strings.Split(bannerArt, "\n") {
		w := displayWidth(line)
		assert.LessOrEqualf(t, w, 100,
			"banner line %d exceeds 100 columns (%d runes): %q", i+1, w, line)
	}
}

func TestPrintBannerWritesOutput(t *testing.T) {
	var buf bytes.Buffer
	printBanner(&buf)

	output := buf.String()
	require.NotEmpty(t, output, "printBanner should produce output when writing to a buffer")
	assert.Contains(t, output, "██", "banner should contain block-drawing characters")
	assert.Contains(t, output, "https://aka.ms/azd-ai-agent-docs", "banner should contain the docs link")
}

func TestPrintBannerAllLinesFit100Cols(t *testing.T) {
	var buf bytes.Buffer
	printBanner(&buf)

	for i, line := range strings.Split(buf.String(), "\n") {
		clean := stripAnsi(line)
		w := displayWidth(clean)
		assert.LessOrEqualf(t, w, 100,
			"rendered line %d exceeds 100 columns (%d runes): %q", i+1, w, clean)
	}
}

// stripAnsi removes ANSI escape sequences from a string.
func stripAnsi(s string) string {
	var out strings.Builder
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
