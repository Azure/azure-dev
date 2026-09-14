// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// WriteDeploymentPreview writes a read-only agent deployment summary.
func WriteDeploymentPreview(out io.Writer, result *DirectDeployPreviewResult) error {
	var text bytes.Buffer
	_, _ = fmt.Fprintf(&text, "Preview: agent %s\n", result.Name)
	if result.Service != "" {
		_, _ = fmt.Fprintf(&text, "Service: %s\n", result.Service)
	}
	if result.Operation == "create" {
		_, _ = fmt.Fprintln(&text, "Agent does not exist; would be created.")
	} else {
		_, _ = fmt.Fprintf(&text, "Current version: %s. Deploy would create a new version.\n", result.CurrentVersion)
	}
	if !result.HasChanges {
		_, _ = fmt.Fprintln(&text, "No changes to agent configuration.")
	}
	if len(result.Sources) > 0 {
		_, _ = fmt.Fprintln(&text, "\nConfiguration sources (highest precedence last):")
		for _, source := range result.Sources {
			_, _ = fmt.Fprintf(&text, "  %s\n", source)
		}
	}
	if err := writeAgentPreviewChanges(&text, result.Changes); err != nil {
		return err
	}
	if result.Image != nil && result.HasImageChanges() {
		_, _ = fmt.Fprintln(&text, "\nContainer image plan")
		_, _ = fmt.Fprintf(&text, "  Mode: %s\n  Build: %t\n  Push: %t\n",
			result.Image.Mode, result.Image.Build, result.Image.Push)
		if result.Image.Image != "" {
			_, _ = fmt.Fprintf(&text, "  Image: %s\n", result.Image.Image)
		} else if !result.Image.Known {
			_, _ = fmt.Fprintln(&text, "  Image: (known after build/provisioning)")
		}
	}
	for _, conflict := range result.SourceConflicts {
		_, _ = fmt.Fprintf(&text, "\nSource precedence: lower-priority values from %s are overridden.\n", conflict.Source)
	}
	_, _ = fmt.Fprintf(&text, "\nSource: %s\n", result.SourcePath)
	for _, note := range result.Notes {
		_, _ = fmt.Fprintln(&text, note)
	}
	_, _ = fmt.Fprintln(&text, "Preview complete. No changes were made.")
	_, err := out.Write(text.Bytes())
	return err
}

func writeAgentPreviewChanges(text *bytes.Buffer, groups []DeployPreviewChangeGroup) error {
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
		_, _ = fmt.Fprintf(text, "\n%s\n", labels[group.Group])
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
				_, _ = fmt.Fprintf(text, "  + %s: %s\n", change.Field, after)
			case "remove":
				_, _ = fmt.Fprintf(text, "  - %s: %s\n", change.Field, before)
			case "modify":
				_, _ = fmt.Fprintf(text, "  ~ %s: %s -> %s\n", change.Field, before, after)
			case "pending":
				_, _ = fmt.Fprintf(text, "  ? %s: (known after deployment)\n", change.Field)
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
