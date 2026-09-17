// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"sort"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/dataset_api"

	"github.com/spf13/cobra"
)

// addTagFilterFlag offers the repeatable filter that narrows a dataset listing.
//
// The service returns every dataset in the project, and a project that
// generates them accumulates hundreds. Tags are how those say what they are, so
// they are also how a reader asks for the ones they meant.
func addTagFilterFlag(cmd *cobra.Command, target *[]string) {
	cmd.Flags().StringArrayVar(target, "tag", nil,
		"Keep only datasets carrying this key=value tag. Repeatable, and "+
			"repeats narrow rather than widen.")
}

// parseTagFilters reads --tag into the set every kept dataset must carry.
//
// Refused locally rather than sent: a filter the service cannot read comes back
// as everything or nothing, and both look like an answer.
func parseTagFilters(raw []string) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	filters := make(map[string]string, len(raw))
	for _, pair := range raw {
		key, value, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, messages.TagFilterNotAPair(pair)
		}
		// The same key twice is two different questions, and keeping the last
		// one silently answers only the second.
		if have, seen := filters[key]; seen && have != value {
			return nil, messages.TagFilterRepeatsKey(key, have, value)
		}
		filters[key] = value
	}
	return filters, nil
}

// matchesTags reports whether a dataset carries every filter.
//
// AND, not OR: `--tag team=support --tag stage=regression` asks for the
// regression datasets belonging to support, and answering it with the union
// returns rows the reader excluded on purpose.
func matchesTags(d dataset_api.Dataset, filters map[string]string) bool {
	for key, want := range filters {
		if have, ok := d.Tags[key]; !ok || have != want {
			return false
		}
	}
	return true
}

// filterByTags keeps the datasets carrying every filter.
func filterByTags(in []dataset_api.Dataset, filters map[string]string) []dataset_api.Dataset {
	if len(filters) == 0 {
		return in
	}
	kept := make([]dataset_api.Dataset, 0, len(in))
	for _, d := range in {
		if matchesTags(d, filters) {
			kept = append(kept, d)
		}
	}
	return kept
}

// applyTagFilter narrows a listing without disturbing an empty one.
func applyTagFilter(
	list *dataset_api.DatasetList,
	filters map[string]string,
) *dataset_api.DatasetList {
	if list == nil || len(filters) == 0 {
		return list
	}
	return &dataset_api.DatasetList{Value: filterByTags(list.Value, filters)}
}

// tagSummary renders a dataset's tags for a cell, ordered so two runs read the
// same way.
func tagSummary(tags map[string]string) string {
	if len(tags) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+tags[key])
	}
	return strings.Join(parts, ", ")
}
