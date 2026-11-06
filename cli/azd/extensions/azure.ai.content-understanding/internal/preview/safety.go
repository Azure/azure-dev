// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package preview

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/net/html"
)

var entity = regexp.MustCompile(`&(?:#[0-9]+|#[xX][0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]+);`)

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

	return source
}
