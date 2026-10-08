// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --output-file used to force the unbounded walk on its own, so `--limit 50
// --output-file rows.json` read every row of the run and wrote all of them.
// The flag was accepted, reported nothing, and did the opposite of what it
// says, which is the shape of bug a caller only finds by counting the file.
//
// Driven through the real flag set rather than a hand-built struct, so a
// renamed or unregistered --limit fails here rather than silently reverting
// the precedence to what it was.
func TestOutputFileDoesNotOverrideAnExplicitLimit(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		walks bool
		why   string
	}{
		{
			name:  "output-file alone takes the whole run",
			args:  []string{"--output-file", "rows.json"},
			walks: true,
			why:   "a file is a bulk destination, so the default is every row",
		},
		{
			name:  "an explicit limit outranks the file's default",
			args:  []string{"--output-file", "rows.json", "--limit", "50"},
			walks: false,
			why:   "--limit is the caller naming a size; writing more is a surprise",
		},
		{
			name:  "an explicit zero still counts as having asked",
			args:  []string{"--output-file", "rows.json", "--limit", "0"},
			walks: false,
			why:   "Changed() rather than the value, or a typed 0 reads as unset",
		},
		{
			name:  "--all wins even against an explicit limit",
			args:  []string{"--output-file", "rows.json", "--limit", "50", "--all"},
			walks: true,
			why:   "--all documents itself as overriding --limit",
		},
		{
			name:  "--all alone still walks without a file",
			args:  []string{"--all"},
			walks: true,
			why:   "--all is the explicit way to ask for everything",
		},
		{
			name:  "a bare listing takes one default page",
			args:  nil,
			walks: false,
			why:   "an unbounded listing floods the terminal",
		},
		{
			name:  "a limit without a file is still one page",
			args:  []string{"--limit", "5"},
			walks: false,
			why:   "nothing here asked for the whole run",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := &runOutputListFlags{}
			cmd := &cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error { return nil }}
			cmd.Flags().StringVar(&flags.outFile, "output-file", "", "")
			addPagingFlags(cmd, &flags.limit, &flags.pageToken, &flags.all, defaultPageSize)
			require.NoError(t, cmd.ParseFlags(tc.args))

			assert.Equal(t, tc.walks, flags.walksEveryPage(cmd), tc.why)
		})
	}
}

// The page size the service is asked for follows from that decision, so the
// two are pinned together: reading every row is a page size of 0, and the
// caller's own limit has to survive as far as the request.
func TestExplicitLimitReachesThePageSize(t *testing.T) {
	flags := &runOutputListFlags{}
	cmd := &cobra.Command{Use: "list", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.Flags().StringVar(&flags.outFile, "output-file", "", "")
	addPagingFlags(cmd, &flags.limit, &flags.pageToken, &flags.all, defaultPageSize)
	require.NoError(t, cmd.ParseFlags([]string{"--output-file", "rows.json", "--limit", "50"}))

	assert.Equal(t, 50, pageSizeOr(flags.limit, flags.walksEveryPage(cmd), defaultPageSize),
		"the limit the caller typed is what the service is asked for")
}
