// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"azureaiskills/internal/pkg/skill_api"
)

const (
	outputJSON  = "json"
	outputTable = "table"
)

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func printSkillDetail(s *skill_api.Skill, format string) error {
	if format == outputJSON {
		return printJSON(s)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintf(tw, "Name\t%s\n", s.Name)
	if s.ID != "" {
		fmt.Fprintf(tw, "ID\t%s\n", s.ID)
	}
	if s.Description != "" {
		fmt.Fprintf(tw, "Description\t%s\n", s.Description)
	}
	if s.DefaultVersion != "" {
		fmt.Fprintf(tw, "Default Version\t%s\n", s.DefaultVersion)
	}
	if s.LatestVersion != "" {
		fmt.Fprintf(tw, "Latest Version\t%s\n", s.LatestVersion)
	}
	if s.CreatedAt != 0 {
		fmt.Fprintf(tw, "Created At\t%s\n", formatUnix(s.CreatedAt))
	}
	return nil
}

func printSkillVersionDetail(v *skill_api.SkillVersion, format string) error {
	if format == outputJSON {
		return printJSON(v)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintf(tw, "Name\t%s\n", v.Name)
	if v.Version != "" {
		fmt.Fprintf(tw, "Version\t%s\n", v.Version)
	}
	if v.ID != "" {
		fmt.Fprintf(tw, "Version ID\t%s\n", v.ID)
	}
	if v.SkillID != "" {
		fmt.Fprintf(tw, "Skill ID\t%s\n", v.SkillID)
	}
	if v.Description != "" {
		fmt.Fprintf(tw, "Description\t%s\n", v.Description)
	}
	if v.CreatedAt != 0 {
		fmt.Fprintf(tw, "Created At\t%s\n", formatUnix(v.CreatedAt))
	}
	return nil
}

func printSkillList(items []skill_api.Skill, format string) error {
	if format == outputJSON {
		if items == nil {
			items = []skill_api.Skill{}
		}
		return printJSON(items)
	}
	return writeSkillTable(os.Stdout, items)
}

func writeSkillTable(w io.Writer, items []skill_api.Skill) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "NAME\tDESCRIPTION\tDEFAULT\tLATEST")
	fmt.Fprintln(tw, "----\t-----------\t-------\t------")
	for _, s := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Name, truncate(s.Description, 60), s.DefaultVersion, s.LatestVersion)
	}
	return nil
}

func formatUnix(seconds int64) string {
	if seconds <= 0 {
		return ""
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	if max < 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
