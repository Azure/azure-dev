// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildLoomInvokeArgsUsesStateAndFlags(t *testing.T) {
	state := rleState{
		EnvironmentId: "env-123",
	}
	flags := &rleInvokeFlags{
		projectEndpoint:            "https://example.services.ai.azure.com/api/projects/p",
		numTasks:                   4,
		modelName:                  "Qwen/Qwen3-32B",
		rendererName:               "qwen3_disable_thinking",
		maxTokens:                  1200,
		loraRank:                   32,
		groupSize:                  4,
		groupsPerBatch:             1,
		maxSteps:                   1,
		lossFn:                     "importance_sampling",
		seed:                       42,
		evalEvery:                  999999,
		saveEvery:                  999999,
		removeConstantRewardGroups: true,
	}

	args := buildLoomInvokeArgs(state, "demo-3", "https://rle.example", flags)

	expected := []string{
		"project_endpoint=https://example.services.ai.azure.com/api/projects/p",
		"env_id=env-123",
		"project=demo-3",
		"control_plane=https://rle.example",
		"num_tasks=4",
		"model_name=Qwen/Qwen3-32B",
		"renderer_name=qwen3_disable_thinking",
		"max_tokens=1200",
		"lora_rank=32",
		"group_size=4",
		"groups_per_batch=1",
		"max_steps=1",
		"loss_fn=importance_sampling",
		"seed=42",
		"eval_every=999999",
		"save_every=999999",
		"remove_constant_reward_groups=true",
	}
	for _, arg := range expected {
		if !slices.Contains(args, arg) {
			t.Fatalf("expected args to contain %q, got %#v", arg, args)
		}
	}
}

func TestBuildLoomInvokeArgsOmitsMaxStepsWhenZero(t *testing.T) {
	state := rleState{
		EnvironmentId: "env-123",
	}
	flags := &rleInvokeFlags{
		projectEndpoint: "https://example.services.ai.azure.com/api/projects/p",
		maxSteps:        0,
	}

	args := buildLoomInvokeArgs(state, "demo-3", "http://localhost:5000", flags)

	if slices.Contains(args, "max_steps=0") {
		t.Fatalf("expected max_steps to be omitted, got %#v", args)
	}
}

func TestLoomRecipeModuleUsesRecipeName(t *testing.T) {
	module := loomRecipeModule("code_rl_with_rle")
	expected := "loom_cookbook.recipes.code_rl_with_rle.train_azure"
	if module != expected {
		t.Fatalf("expected %q, got %q", expected, module)
	}
}

func TestEnsureLoomRecipeExistsRequiresTrainAzureEntrypoint(t *testing.T) {
	root := t.TempDir()
	recipeDir := filepath.Join(root, "loom_cookbook", "recipes", "code_rl_with_rle")
	if err := os.MkdirAll(recipeDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recipeDir, "train_azure.py"), []byte(""), 0600); err != nil {
		t.Fatal(err)
	}

	if err := ensureLoomRecipeExists(root, "code_rl_with_rle"); err != nil {
		t.Fatal(err)
	}
	if err := ensureLoomRecipeExists(root, "missing_recipe"); err == nil {
		t.Fatal("expected missing recipe to fail")
	}
}

func TestEnsureLoomLocalSourcesAddsLocalSources(t *testing.T) {
	root := t.TempDir()
	cookbookPath := filepath.Join(root, ".azd-rle", "recipes", "loom", "loom-cookbook")
	if err := os.MkdirAll(cookbookPath, 0700); err != nil {
		t.Fatal(err)
	}
	pyprojectPath := filepath.Join(cookbookPath, "pyproject.toml")
	if err := os.WriteFile(pyprojectPath, []byte("[project]\nname = \"loom-cookbook\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rleSdkPackage := filepath.Join(root, ".azd-rle", "deps", "rle_sdk-0.1.3-py3-none-any.whl")

	if err := ensureLoomLocalSources(cookbookPath, rleSdkPackage); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(pyprojectPath)
	if err != nil {
		t.Fatal(err)
	}
	contents := string(data)
	expectedSources := []string{
		"[tool.uv.sources]",
		"azure-ai-finetuning-sessions = { path = \"../azure-ai-finetuning-sessions\" }",
		"rle-sdk = { path = \"../../../deps/rle_sdk-0.1.3-py3-none-any.whl\" }",
	}
	for _, expected := range expectedSources {
		if !strings.Contains(contents, expected) {
			t.Fatalf("expected pyproject to contain %q, got %s", expected, contents)
		}
	}
}
