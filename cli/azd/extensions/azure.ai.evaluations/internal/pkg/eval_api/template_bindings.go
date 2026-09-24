// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"regexp"
	"sort"
)

// itemBinding matches the {{item.<field>}} placeholders a message template uses
// to read a column out of each dataset row.
var itemBinding = regexp.MustCompile(`\{\{\s*item\.([A-Za-z0-9_]+)\s*\}\}`)

// TemplateItemFields returns the dataset columns this data source's message
// template reads, sorted and deduplicated.
//
// A template binding a column the rows do not have is not an error the service
// reports: it invokes the target with an empty value and scores whatever comes
// back, which reads as a low-scoring target rather than a request that asked
// for a column that was never there. Knowing what a data source binds is what
// lets the caller refuse before that happens.
func (ds *EvalRunDataSource) TemplateItemFields() []string {
	if ds == nil || ds.InputMessages == nil {
		return nil
	}

	seen := map[string]struct{}{}
	for _, tmpl := range ds.InputMessages.Template {
		for _, match := range itemBinding.FindAllStringSubmatch(tmpl.Content, -1) {
			seen[match[1]] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}

	fields := make([]string, 0, len(seen))
	for field := range seen {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

// MissingTemplateFields returns the columns this data source's template binds
// that no row in items carries.
//
// Absence is judged across the whole set rather than per row: a column some
// rows omit is a sparse dataset, which is the caller's business, while a column
// no row has at all is a request that cannot be answered.
func (ds *EvalRunDataSource) MissingTemplateFields(items []map[string]any) []string {
	bound := ds.TemplateItemFields()
	if len(bound) == 0 {
		return nil
	}

	present := map[string]struct{}{}
	for _, item := range items {
		for key := range item {
			present[key] = struct{}{}
		}
	}

	var missing []string
	for _, field := range bound {
		if _, ok := present[field]; !ok {
			missing = append(missing, field)
		}
	}
	return missing
}
