// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

func TestModelCatalog256KRendererParity(t *testing.T) {
	data, err := os.ReadFile("models.json")
	if err != nil {
		t.Fatal(err)
	}
	var models []struct {
		Model     string   `json:"model"`
		Renderers []string `json:"renderers"`
	}
	if err := json.Unmarshal(data, &models); err != nil {
		t.Fatal(err)
	}
	renderers := make(map[string][]string, len(models))
	for _, model := range models {
		if _, exists := renderers[model.Model]; exists {
			t.Fatalf("duplicate model %q", model.Model)
		}
		renderers[model.Model] = model.Renderers
	}
	for _, base := range []string{
		"Qwen/Qwen3.8-27B",
		"nvidia/NVIDIA-Nemotron-3-Ultra-550B-A55B-BF16",
	} {
		t.Run(base, func(t *testing.T) {
			want := renderers[base]
			if len(want) == 0 {
				t.Fatalf("base model %q has no renderers", base)
			}
			variant := base + "-256K"
			if got := renderers[variant]; !slices.Equal(got, want) {
				t.Fatalf("%s renderers = %v, want base model renderers %v", variant, got, want)
			}
		})
	}
}
