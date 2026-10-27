// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// These projections are used only for human-readable output. JSON output always
// uses the original RawMessage so new fields and arbitrary-precision numbers survive.
type taskTableProjection struct {
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	AgentName string          `json:"agent_name"`
	SessionID string          `json:"session_id"`
	UpdatedAt json.RawMessage `json:"updated_at"`
}

type taskDetailProjection struct {
	taskTableProjection
	CreatedAt   json.RawMessage `json:"created_at"`
	StartedAt   json.RawMessage `json:"started_at"`
	CompletedAt json.RawMessage `json:"completed_at"`
	Lease       *struct {
		Owner     string          `json:"owner"`
		ExpiresAt json.RawMessage `json:"expires_at"`
	} `json:"lease"`
	Error json.RawMessage `json:"error"`
}

type taskTableRow struct {
	ID, Status, Agent, Session, Updated string
}

func taskDisplayText(value string) string {
	// Escape controls, including C1 and bidi/format controls, rather than allowing
	// service strings to issue terminal commands or forge extra output rows.
	var safe strings.Builder
	for _, r := range value {
		if safe.Len() >= 256 {
			safe.WriteString("...")
			break
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			quoted := strconv.QuoteRuneToASCII(r)
			safe.WriteString(quoted[1 : len(quoted)-1])
		} else {
			safe.WriteRune(r)
		}
	}
	return safe.String()
}

func taskTableTimestamp(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return taskDisplayText(text)
	}
	var seconds int64
	if json.Unmarshal(raw, &seconds) == nil {
		if seconds == 0 {
			return ""
		}
		// Avoid unbounded dates or converting unknown numeric timestamp formats.
		if seconds > 0 && seconds <= 253402300799 {
			return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
		}
	}
	return ""
}

func writeTasksTable(writer io.Writer, raw json.RawMessage, list bool) error {
	return writeTasksTableWithFormatter(writer, raw, list, &output.PrettyTableFormatter{})
}

func writeTasksTableWithFormatter(
	writer io.Writer, raw json.RawMessage, list bool, formatter *output.PrettyTableFormatter,
) error {
	var tasks []taskTableProjection
	var detail taskDetailProjection
	var hasMore bool
	if list {
		var page struct {
			Data    []taskTableProjection `json:"data"`
			HasMore bool                  `json:"has_more"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return fmt.Errorf("invalid Tasks table response")
		}
		tasks, hasMore = page.Data, page.HasMore
	} else {
		if err := json.Unmarshal(raw, &detail); err != nil {
			return fmt.Errorf("invalid Task table response")
		}
		tasks = []taskTableProjection{detail.taskTableProjection}
	}
	rows := make([]taskTableRow, 0, len(tasks))
	for _, task := range tasks {
		rows = append(rows, taskTableRow{
			ID: taskDisplayText(task.ID), Status: taskDisplayText(task.Status),
			Agent: taskDisplayText(task.AgentName), Session: taskDisplayText(task.SessionID),
			Updated: taskTableTimestamp(task.UpdatedAt),
		})
	}
	if len(rows) == 0 {
		if _, err := fmt.Fprintln(writer, "No Tasks on this page."); err != nil {
			return err
		}
	} else {
		if err := formatter.Format(rows, writer, output.PrettyTableFormatterOptions{
			Columns: []output.PrettyColumn{
				{Column: output.Column{Heading: "ID", ValueTemplate: "{{if .ID}}{{.ID}}{{else}}-{{end}}"},
					CardTitle: true, Truncatable: true},
				{Column: output.Column{Heading: "STATUS", ValueTemplate: "{{if .Status}}{{.Status}}{{else}}-{{end}}"},
					Priority: 1, Truncatable: true},
				{Column: output.Column{Heading: "AGENT", ValueTemplate: "{{if .Agent}}{{.Agent}}{{else}}-{{end}}"},
					Priority: 2, Truncatable: true},
				{Column: output.Column{Heading: "SESSION", ValueTemplate: "{{if .Session}}{{.Session}}{{else}}-{{end}}"},
					Priority: 3, Truncatable: true},
				{Column: output.Column{Heading: "UPDATED", ValueTemplate: "{{if .Updated}}{{.Updated}}{{else}}-{{end}}"},
					Priority: 2, Truncatable: true},
			}, ResponsiveColumnHint: true, ForceCards: !list,
		}); err != nil {
			return err
		}
	}
	if !list {
		task := detail
		var leaseOwner, leaseExpiry string
		if task.Lease != nil {
			leaseOwner = taskDisplayText(task.Lease.Owner)
			leaseExpiry = taskTableTimestamp(task.Lease.ExpiresAt)
		}
		for _, field := range []struct{ label, value string }{
			{"Created", taskTableTimestamp(task.CreatedAt)},
			{"Started", taskTableTimestamp(task.StartedAt)},
			{"Completed", taskTableTimestamp(task.CompletedAt)},
			{"Lease owner", leaseOwner},
			{"Lease expiry", leaseExpiry},
		} {
			value := field.value
			if value == "" {
				value = "-"
			}
			if _, err := fmt.Fprintf(writer, "%s: %s\n", field.label, value); err != nil {
				return err
			}
		}
		hasError := len(task.Error) > 0 && string(task.Error) != "null"
		if _, err := fmt.Fprintf(writer, "Has error: %t\n", hasError); err != nil {
			return err
		}
	}
	if hasMore {
		_, err := fmt.Fprintln(writer, "\nMore Tasks are available. Use --output json to read opaque last_id, "+
			"then pass it unchanged to --after. Keep the same filters, --order, and --limit.")
		return err
	}
	return nil
}
