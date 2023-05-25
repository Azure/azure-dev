// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"fmt"
	"log"
	"strings"
)

// generates text from a list of strings, for example:
// ["foo"]        					=> "foo"
// ["foo", "bar"] 					=> "foo and bar"
// ["foo", "bar", "axe"] 			=> "foo, bar and axe"
// ["foo", "bar", ..., ..., "axe"] 	=> "foo, bar, ..., ... and axe"
func ListAsText(items []string) string {
	count := len(items)
	if count < 1 {
		log.Panic("calling itemsCountAsText() with empty list.")
	}

	if count == 1 {
		return items[0]
	}

	if count == 2 {
		return fmt.Sprintf("%s and %s", items[0], items[1])
	}

	return fmt.Sprintf("%s and %s", strings.Join(items[:count-1], ", "), items[count-1])
}
