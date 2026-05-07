// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVisibleLength(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"empty string", "", 0},
		{"plain ASCII", "hello", 5},
		{"single char", "x", 1},
		{"with spaces", "a b c", 5},
		{
			"single ANSI color code",
			"\x1b[31mred\x1b[0m",
			3,
		},
		{
			"multiple ANSI codes",
			"\x1b[1m\x1b[31mbold red\x1b[0m",
			8,
		},
		{
			"nested ANSI codes around text",
			"\x1b[32mgreen\x1b[0m and \x1b[34mblue\x1b[0m",
			14,
		},
		{
			"ANSI with no visible text",
			"\x1b[0m\x1b[1m\x1b[0m",
			0,
		},
		{"unicode characters", "héllo", 5},
		{"CJK characters", "日本語", 3},
		{"emoji single codepoint", "★", 1},
		{
			"mixed ANSI and unicode",
			"\x1b[36m日本\x1b[0m",
			2,
		},
		{"tab and printable", "a\tb", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VisibleLength(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCountLineBreaks(t *testing.T) {
	tests := []struct {
		name    string
		content string
		width   int
		want    int
	}{
		{"empty string", "", 80, 0},
		{"no newlines short", "hello", 80, 0},
		{
			"single newline",
			"hello\n",
			80, 1,
		},
		{
			"two newlines",
			"line1\nline2\n",
			80, 2,
		},
		{
			"wrapping single line",
			"abcdefghij",
			5, 1,
		},
		{
			"exact width no wrap",
			"abcde",
			5, 0,
		},
		{
			"wrapping twice",
			"abcdefghijklmno",
			5, 2,
		},
		{
			"newline plus wrapping",
			"abcdefghij\nab",
			5, 2,
		},
		{
			"multiple lines with wrapping",
			"abcdefghij\nabcdefghij\n",
			5, 4,
		},
		{
			"width of 1",
			"abc",
			1, 2,
		},
		{
			"ANSI codes not counted for wrap",
			"\x1b[31m" + "ab" + "\x1b[0m",
			10, 0,
		},
		{
			"only newlines",
			"\n\n\n",
			80, 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CountLineBreaks(tt.content, tt.width)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSpecialTextRegex(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"removes single color code",
			"\x1b[31mtext\x1b[0m",
			"text",
		},
		{
			"removes bold code",
			"\x1b[1mbold\x1b[0m",
			"bold",
		},
		{
			"removes compound codes",
			"\x1b[1;31mbold red\x1b[0m",
			"bold red",
		},
		{
			"no codes unchanged",
			"plain text",
			"plain text",
		},
		{
			"empty string unchanged",
			"",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := specialTextRegex.ReplaceAllString(tt.input, "")
			assert.Equal(t, tt.want, got)
		})
	}
}
