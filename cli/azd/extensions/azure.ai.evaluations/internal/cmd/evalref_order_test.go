// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
)

// `delete` refuses a name carried by several evals and lists the ids, so the
// order those ids come back in is part of a message a reader acts on. The
// service does not promise an order, and this used to hand back whatever the
// listing happened to contain while the comment above it claimed newest-first.
func TestEvalIDsNamedSortsNewestFirst(t *testing.T) {
	list := &eval_api.OpenAIEvalList{Data: []eval_api.OpenAIEval{
		{ID: "eval_oldest", Name: "shared", CreatedAt: "2026-01-01T00:00:00Z"},
		{ID: "eval_newest", Name: "shared", CreatedAt: "2026-08-01T00:00:00Z"},
		{ID: "eval_middle", Name: "shared", CreatedAt: "2026-04-01T00:00:00Z"},
		{ID: "eval_other", Name: "different", CreatedAt: "2026-09-01T00:00:00Z"},
	}}

	got := idsNamedIn(list, "shared")

	assert.Equal(t, []string{"eval_newest", "eval_middle", "eval_oldest"}, got)
}

// The service spells created_at as epoch seconds on some routes and RFC3339 on
// others, so both have to order the same way.
func TestEvalIDsNamedSortsAcrossTimestampShapes(t *testing.T) {
	list := &eval_api.OpenAIEvalList{Data: []eval_api.OpenAIEval{
		{ID: "eval_old", Name: "shared", CreatedAt: float64(1767225600)}, // 2026-01-01
		{ID: "eval_new", Name: "shared", CreatedAt: "2026-08-01T00:00:00Z"},
	}}

	assert.Equal(t, []string{"eval_new", "eval_old"}, idsNamedIn(list, "shared"))
}

// An eval the service described without a usable timestamp must not win by
// accident; it sorts last and the ones that can be ordered still are.
func TestEvalIDsNamedPutsUndatedLast(t *testing.T) {
	list := &eval_api.OpenAIEvalList{Data: []eval_api.OpenAIEval{
		{ID: "eval_undated", Name: "shared"},
		{ID: "eval_dated", Name: "shared", CreatedAt: "2026-01-01T00:00:00Z"},
	}}

	assert.Equal(t, []string{"eval_dated", "eval_undated"}, idsNamedIn(list, "shared"))
}
