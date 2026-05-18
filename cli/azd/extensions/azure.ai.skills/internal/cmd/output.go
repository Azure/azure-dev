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
	if s.SkillID != "" {
		fmt.Fprintf(tw, "Skill ID\t%s\n", s.SkillID)
	}
	if s.Description != "" {
		fmt.Fprintf(tw, "Description\t%s\n", s.Description)
	}
	fmt.Fprintf(tw, "Has Blob\t%t\n", s.HasBlob)
	if len(s.Metadata) > 0 {
		fmt.Fprintln(tw, "Metadata\t")
		for k, v := range s.Metadata {
			fmt.Fprintf(tw, "  %s\t%s\n", k, v)
		}
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
	fmt.Fprintln(tw, "NAME\tDESCRIPTION\tHAS BLOB")
	fmt.Fprintln(tw, "----\t-----------\t--------")
	for _, s := range items {
		fmt.Fprintf(tw, "%s\t%s\t%t\n", s.Name, truncate(s.Description, 60), s.HasBlob)
	}
	return nil
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
