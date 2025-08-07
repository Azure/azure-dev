// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package stringutil

import (
	"slices"
	"testing"
)

func TestCompareLowerSort(t *testing.T) {
	unordered := []string{
		"Zebra",
		"apple",
		"applesauce",
		"Banana",
		"CHERRY",
		"date",
		"",
		"Apple",
		"café",
		"cafe",
		"APPLE",
	}

	expected := []string{
		"",
		"apple",
		"Apple",
		"APPLE",
		"applesauce",
		"Banana",
		"cafe",
		"café",
		"CHERRY",
		"date",
		"Zebra",
	}

	slices.SortFunc(unordered, func(a, b string) int {
		return CompareLower(a, b)
	})

	if !slices.Equal(unordered, expected) {
		t.Errorf("incorrect sort order:\ngot:  %q\nwant: %q", unordered, expected)
	}
}
