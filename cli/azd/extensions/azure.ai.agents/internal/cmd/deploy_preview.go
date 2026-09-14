// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"azureaiagent/internal/project"
)

func writeAgentDeployPreview(out io.Writer, format string, result *project.DirectDeployPreviewResult) error {
	if format == "json" {
		return emitJSONTo(out, result)
	}

	var text bytes.Buffer
	fmt.Fprintf(&text, "Dry run: agent %s\n", result.Name)
	if result.Service != "" {
		fmt.Fprintf(&text, "Service: %s\n", result.Service)
	}
	if result.Operation == "create" {
		fmt.Fprintln(&text, "Agent does not exist; would be created.")
	} else {
		fmt.Fprintf(&text, "Current version: %s. Deploy would create a new version.\n", result.CurrentVersion)
	}
	if !result.HasChanges {
		fmt.Fprintln(&text, "No changes to agent configuration.")
	}
	if len(result.Sources) > 0 {
		fmt.Fprintln(&text, "\nConfiguration sources (highest precedence last):")
		for _, source := range result.Sources {
			fmt.Fprintf(&text, "  %s\n", source)
		}
	}
	if err := writeAgentPreviewChanges(&text, result.Changes); err != nil {
		return err
	}
	if result.Image != nil && result.HasImageChanges() {
		fmt.Fprintln(&text, "\nContainer image plan")
		fmt.Fprintf(&text, "  Mode: %s\n  Build: %t\n  Push: %t\n",
			result.Image.Mode, result.Image.Build, result.Image.Push)
		if result.Image.Image != "" {
			fmt.Fprintf(&text, "  Image: %s\n", result.Image.Image)
		} else if !result.Image.Known {
			fmt.Fprintln(&text, "  Image: (known after build/provisioning)")
		}
	}
	for _, conflict := range result.SourceConflicts {
		fmt.Fprintf(&text, "\nSource precedence: lower-priority values from %s are overridden.\n", conflict.Source)
	}
	fmt.Fprintf(&text, "\nSource: %s\n", result.SourcePath)
	for _, note := range result.Notes {
		fmt.Fprintln(&text, note)
	}
	fmt.Fprintln(&text, "Dry run complete. No changes were made.")
	_, err := out.Write(text.Bytes())
	return err
}

func writeAgentPreviewChanges(text *bytes.Buffer, groups []project.DeployPreviewChangeGroup) error {
	labels := map[string]string{
		"metadata":             "Metadata",
		"protocols":            "Protocols",
		"resources":            "Resources",
		"environmentVariables": "Environment variables (values redacted)",
		"modelDeployment":      "Model deployment reference",
		"containerImage":       "Container image",
		"code":                 "Code configuration",
		"session":              "Session configuration",
		"contentSafety":        "Content safety",
		"endpoint":             "Agent endpoint",
		"agentCard":            "Agent card",
	}
	for _, group := range groups {
		fmt.Fprintf(text, "\n%s\n", labels[group.Group])
		for _, change := range group.Changes {
			before, err := formatDeployPreviewValue(change.Before)
			if err != nil {
				return err
			}
			after, err := formatDeployPreviewValue(change.After)
			if err != nil {
				return err
			}
			switch change.Kind {
			case "add":
				fmt.Fprintf(text, "  + %s: %s\n", change.Field, after)
			case "remove":
				fmt.Fprintf(text, "  - %s: %s\n", change.Field, before)
			case "modify":
				fmt.Fprintf(text, "  ~ %s: %s -> %s\n", change.Field, before, after)
			case "pending":
				fmt.Fprintf(text, "  ? %s: (known after deployment)\n", change.Field)
			default:
				return fmt.Errorf("unknown deployment preview change kind %q", change.Kind)
			}
		}
	}
	return nil
}

func formatDeployPreviewValue(value any) (string, error) {
	if value == nil {
		return "(not set)", nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("format deployment preview value: %w", err)
	}
	return string(data), nil
}
