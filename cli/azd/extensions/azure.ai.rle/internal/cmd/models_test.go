// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestModelsListsSupportedModelsAndRenderers(t *testing.T) {
	outputFormat := "default"
	command := newModelsCommand(&outputFormat)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)

	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	for _, expected := range []string{
		"MODEL", "RENDERERS",
		"MAI/MAI-Code-1.1-Flash", "mai_harmony_internal",
		"Qwen/Qwen3-32B", "qwen3_ext",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("expected output to contain %q, got %s", expected, output.String())
		}
	}
}

func TestModelsSupportsJSONOutput(t *testing.T) {
	outputFormat := "json"
	command := newModelsCommand(&outputFormat)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)

	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}

	var models []supportedModel
	if err := json.Unmarshal(output.Bytes(), &models); err != nil {
		t.Fatalf("expected JSON output, got %s: %v", output.String(), err)
	}
	if len(models) != 2 || models[0].Model != "MAI/MAI-Code-1.1-Flash" ||
		len(models[0].Renderers) != 1 || models[0].Renderers[0] != "mai_harmony_internal" ||
		models[1].Model != "Qwen/Qwen3-32B" || len(models[1].Renderers) != 1 ||
		models[1].Renderers[0] != "qwen3_ext" {
		t.Fatalf("unexpected models JSON: %#v", models)
	}
}
