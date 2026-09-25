// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type stateStoreTableRow struct {
	Name     string
	ETag     string
	Updated  string
	Isolated bool
	TTL      int64
	Deleted  bool
}

func writeStateStoreTable(writer io.Writer, result any) error {
	var rows []stateStoreTableRow
	var columns []output.PrettyColumn
	var hasMore bool
	var last *string
	var itemDetail *agent_api.StateStoreItem
	storeColumns := []output.PrettyColumn{
		{Column: output.Column{Heading: "NAME", ValueTemplate: "{{.Name}}"}, CardTitle: true, Wrappable: true},
		{Column: output.Column{Heading: "USER ISOLATION", ValueTemplate: "{{.Isolated}}"}, Priority: 2},
		{Column: output.Column{Heading: "TTL (SECONDS)", ValueTemplate: "{{.TTL}}"}, Priority: 3},
		{Column: output.Column{Heading: "UPDATED", ValueTemplate: "{{.Updated}}"}, Priority: 2},
	}
	itemColumns := []output.PrettyColumn{
		{Column: output.Column{Heading: "KEY", ValueTemplate: "{{.Name}}"}, CardTitle: true, Wrappable: true},
		{Column: output.Column{Heading: "ETAG", ValueTemplate: "{{.ETag}}"}, Priority: 3, Truncatable: true},
		{Column: output.Column{Heading: "UPDATED", ValueTemplate: "{{.Updated}}"}, Priority: 2},
	}
	switch v := result.(type) {
	case *agent_api.StateStorePage[agent_api.StateStore]:
		columns = storeColumns
		for _, store := range v.Data {
			rows = append(rows, stateStoreRow(store))
		}
		hasMore, last = v.HasMore, v.LastID
	case *agent_api.StateStore:
		columns, rows = storeColumns, []stateStoreTableRow{stateStoreRow(*v)}
	case *agent_api.StateStorePage[agent_api.StateStoreItem]:
		columns = itemColumns
		for _, item := range v.Data {
			rows = append(rows, stateStoreItemRow(item))
		}
		hasMore, last = v.HasMore, v.LastID
	case *agent_api.StateStoreItem:
		columns = []output.PrettyColumn{itemColumns[0], itemColumns[2]}
		rows = []stateStoreTableRow{stateStoreItemRow(*v)}
		itemDetail = v
	case *agent_api.DeletedStateStoreItem:
		columns = []output.PrettyColumn{
			{Column: output.Column{Heading: "KEY", ValueTemplate: "{{.Name}}"}, CardTitle: true, Wrappable: true},
			{Column: output.Column{Heading: "DELETED", ValueTemplate: "{{.Deleted}}"}},
		}
		rows = []stateStoreTableRow{{Name: stateStoreDisplayText(v.Key), Deleted: v.Deleted}}
	default:
		return fmt.Errorf("unsupported State Store table result")
	}
	if len(rows) == 0 {
		if _, err := fmt.Fprintln(writer, "No results on this page."); err != nil {
			return err
		}
	} else {
		formatter := &output.PrettyTableFormatter{}
		if err := formatter.Format(rows, writer, output.PrettyTableFormatterOptions{
			Columns: columns, ResponsiveColumnHint: true,
		}); err != nil {
			return err
		}
	}
	if itemDetail != nil {
		// Detail output must retain the complete concurrency token, even on narrow terminals.
		if _, err := fmt.Fprintf(writer, "\nETag: %s\n", itemDetail.ETag); err != nil {
			return err
		}
		if len(itemDetail.Tags) > 0 {
			tags, err := json.Marshal(itemDetail.Tags)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(writer, "Tags: %s\n", tags); err != nil {
				return err
			}
		}
		value := itemDetail.Value
		if len(value) > 0 && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			var formatted bytes.Buffer
			if err := json.Indent(&formatted, value, "", "  "); err != nil {
				return fmt.Errorf("invalid item value in response")
			}
			if _, err := fmt.Fprintf(writer, "\nValue:\n%s\n", formatted.String()); err != nil {
				return err
			}
		}
	}
	if hasMore {
		message := "\nMore results are available. Keep the same --order and --limit when paging."
		if _, err := fmt.Fprintln(writer, message); err != nil {
			return err
		}
		if last != nil {
			_, err := fmt.Fprintf(writer, "Next page: pass --after %q\n", *last)
			return err
		}
	}
	return nil
}

func stateStoreDisplayText(value string) string {
	quoted := strconv.Quote(value)
	return quoted[1 : len(quoted)-1]
}

func stateStoreRow(store agent_api.StateStore) stateStoreTableRow {
	return stateStoreTableRow{
		Name: stateStoreDisplayText(store.Name), Isolated: store.UserIsolation, TTL: store.ItemTTLSeconds,
		Updated: stateStoreTimestamp(store.UpdatedAt),
	}
}

func stateStoreItemRow(item agent_api.StateStoreItem) stateStoreTableRow {
	return stateStoreTableRow{
		Name: stateStoreDisplayText(item.Key), ETag: item.ETag,
		Updated: stateStoreTimestamp(item.UpdatedAt),
	}
}

func stateStoreTimestamp(seconds int64) string {
	if seconds == 0 {
		return "-"
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}
