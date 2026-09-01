// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Paging is one contract, so a reader who learns it on one listing has learned
// it on the others.
//
// --limit on its own truncates: it caps the rows and says nothing about what is
// past them, which is what `eval list` and `run list` were doing. A page is only
// usable if the next one is reachable.
func TestListingsShareOnePagingContract(t *testing.T) {
	for _, name := range []string{"list", "run list", "run output list"} {
		t.Run(name, func(t *testing.T) {
			cmd := find(t, name)

			limit := cmd.Flags().Lookup("limit")
			require.NotNilf(t, limit, "%s cannot bound a page", name)

			token := cmd.Flags().Lookup("after")
			require.NotNilf(t, token, "%s caps the rows but cannot resume past them", name)

			all := cmd.Flags().Lookup("all")
			require.NotNilf(t, all, "%s has no explicit way to ask for everything", name)
			assert.Equal(t, "false", all.DefValue,
				"a listing must not flood the terminal unless it was asked to")

			// The old spelling was renamed for one vocabulary across commands.
			assert.Nil(t, cmd.Flags().Lookup("pagination-token"),
				"two names for the same cursor is one to get wrong")
		})
	}
}

// --all means follow the cursor, not "a very large page".
func TestPageSizeOr(t *testing.T) {
	assert.Equal(t, 50, pageSizeOr(0, false, 50), "no limit takes the default page")
	assert.Equal(t, 10, pageSizeOr(10, false, 50), "an explicit limit wins")
	assert.Equal(t, 0, pageSizeOr(10, true, 50),
		"--all asks the walker for everything rather than one big page")
}
