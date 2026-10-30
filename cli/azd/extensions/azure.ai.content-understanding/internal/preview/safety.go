// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package preview

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/net/html"
)

var (
	previewURL = regexp.MustCompile("(?i)\\b[a-z][a-z0-9+.-]*://[^\\s\\x00-\\x1f<>\"'`]+")
	entity     = regexp.MustCompile(`&(?:#[0-9]+|#[xX][0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]+);`)
)

func safeText(source string) string {
	clean := func(value string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' || unicode.Is(unicode.Bidi_Control, r) {
				return -1
			}
			return r
		}, ansi.Strip(value))
	}
	source = clean(strings.ReplaceAll(source, "\r\n", "\n"))
	// Encoded terminal controls must not become active when Markdown/HTML decodes them.
	source = entity.ReplaceAllStringFunc(source, func(value string) string {
		decoded := html.UnescapeString(value)
		if clean(decoded) != decoded {
			return clean(decoded)
		}
		return value
	})

	// Match decoded URLs, but map replacements back to source offsets so escaped
	// Markdown/HTML elsewhere is not accidentally promoted to active markup.
	var decoded strings.Builder
	offsets := make([]int, 0, len(source)+1)
	cursor := 0
	for _, match := range entity.FindAllStringIndex(source, -1) {
		for index := cursor; index < match[0]; index++ {
			decoded.WriteByte(source[index])
			offsets = append(offsets, index)
		}
		value := html.UnescapeString(source[match[0]:match[1]])
		decoded.WriteString(value)
		for range len(value) {
			offsets = append(offsets, match[0])
		}
		cursor = match[1]
	}
	for index := cursor; index < len(source); index++ {
		decoded.WriteByte(source[index])
		offsets = append(offsets, index)
	}
	offsets = append(offsets, len(source))
	text := decoded.String()
	var result strings.Builder
	cursor = 0
	for _, match := range previewURL.FindAllStringIndex(text, -1) {
		raw := strings.TrimRight(text[match[0]:match[1]], ").,;:!]}")
		end := match[0] + len(raw)
		parsed, err := url.Parse(raw)
		replacement := "[redacted URL]"
		if err == nil && parsed.Host != "" {
			parsed.User = nil
			parsed.RawQuery, parsed.Fragment, parsed.RawFragment = "", "", ""
			parsed.ForceQuery = false
			replacement = parsed.String()
		}
		if replacement == raw {
			continue
		}
		result.WriteString(source[cursor:offsets[match[0]]])
		result.WriteString(replacement)
		cursor = offsets[end]
	}
	result.WriteString(source[cursor:])
	return result.String()
}
