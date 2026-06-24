// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "testing"

func TestResolveRecipeImageDerivesFromEnvName(t *testing.T) {
	image, err := resolveRecipeImage(defaultRecipeName, "")
	if err != nil {
		t.Fatal(err)
	}
	if image != "" {
		t.Fatalf("expected empty image so it is derived from the environment name, got %q", image)
	}
}

func TestResolveRecipeImageAllowsOverride(t *testing.T) {
	image, err := resolveRecipeImage("unknown", "example.azurecr.io/custom:latest")
	if err != nil {
		t.Fatal(err)
	}
	if image != "example.azurecr.io/custom:latest" {
		t.Fatalf("expected override image, got %q", image)
	}
}

func TestResolveRecipeImageRejectsUnknownRecipe(t *testing.T) {
	if _, err := resolveRecipeImage("unknown", ""); err == nil {
		t.Fatal("expected unknown recipe to fail")
	}
}
