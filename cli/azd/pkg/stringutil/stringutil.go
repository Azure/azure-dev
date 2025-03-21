// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package stringutil

import (
	"unicode"
	"unicode/utf8"
)

// CompareLower returns an integer comparing two strings in a case-insensitive manner.
// The result will be 0 if a == b, -1 if a < b, and +1 if a > b.
//
// This comparison does not obey locale-specific rules.
// To compare strings based on case-insensitive, locale-specific ordering, see [golang.org/x/text/collate].
func CompareLower(a, b string) int {
	for {
		rb, nb := utf8.DecodeRuneInString(b)
		if nb == 0 {
			// len(a) > len(b), a > b.
			return 1
		}

		ra, na := utf8.DecodeRuneInString(a)
		if na == 0 {
			// len(b) > len(a), b > a.
			return -1
		}

		rb = unicode.ToLower(rb)
		ra = unicode.ToLower(ra)

		if ra > rb {
			return 1
		} else if ra < rb {
			return -1
		}

		// Trim slices to the next rune.
		a = a[na:]
		b = b[nb:]
	}
}
