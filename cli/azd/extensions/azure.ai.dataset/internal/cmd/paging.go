// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// defaultPageSize keeps a first page readable on a shared project, which runs
// to hundreds of datasets. Walking all of them buried the reader's own rows.
const defaultPageSize = 20

// addDisplayPagingFlags bounds a listing that already holds every row.
//
// These listings walk their next links before rendering, so there is no cursor
// to resume from and offering one would be a lie. What was missing is both
// halves: nothing bounded the output, and 250 rows arrived with nothing saying
// whether that was all of them.
func addDisplayPagingFlags(cmd *cobra.Command, limit *int, all *bool, defaultPage int) {
	cmd.Flags().IntVar(limit, "limit", 0,
		fmt.Sprintf("Rows to show. Defaults to %d.", defaultPage))
	cmd.Flags().BoolVar(all, "all", false, "Show every row.")
}

// trimForDisplay cuts a fully-fetched listing to one page and reports the total.
//
// It trims `-o json` too, because emitJSONPage's envelope says how much was
// left out. A bare array could not, which is why the flag had to be ignored
// there or risk a script reading a short list as the whole collection.
func trimForDisplay[T any](cmd *cobra.Command, rows []T) (shown []T, total int, trimmed bool) {
	total = len(rows)

	all, _ := cmd.Flags().GetBool("all")
	if all {
		return rows, total, false
	}
	limit, err := cmd.Flags().GetInt("limit")
	if err != nil {
		return rows, total, false
	}
	if limit <= 0 {
		limit = defaultPageSize
	}
	if total <= limit {
		return rows, total, false
	}
	return rows[:limit], total, true
}
