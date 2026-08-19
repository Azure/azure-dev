// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func TestNormalizeVersionBumpFlag(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		expected string
	}{
		{name: "default major", value: "major", expected: "Major"},
		{name: "minor", value: "minor", expected: "Minor"},
		{name: "patch", value: "patch", expected: "Patch"},
		{name: "trimmed uppercase", value: "  MAJOR  ", expected: "Major"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeVersionBumpFlag(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestNormalizeVersionBumpFlagRejectsInvalidValue(t *testing.T) {
	_, err := normalizeVersionBumpFlag("gold")
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_invalid_version_bump" {
		t.Fatalf("expected invalid version bump code, got %q", localErr.Code)
	}
}

func TestPublishRejectsInvalidVersionBumpBeforeResolvingState(t *testing.T) {
	tempDir := t.TempDir()
	t.Chdir(tempDir)

	command := newPublishCommand()
	command.SetArgs([]string{"--version-bump", "gold"})
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})

	err := command.Execute()
	localErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected LocalError, got %T", err)
	}
	if localErr.Code != "rle_invalid_version_bump" {
		t.Fatalf("expected invalid version bump code, got %q", localErr.Code)
	}
}

func TestBuildEnvironmentCreateRequestIncludesVersionBump(t *testing.T) {
	request := buildEnvironmentCreateRequest("echo_env", "example.azurecr.io/echo_env:latest", "Patch")
	if request.VersionBump != "Patch" {
		t.Fatalf("expected version bump to be included, got %#v", request)
	}
}

func TestEnvironmentOutputUsesEnvironmentNameField(t *testing.T) {
	body, err := json.Marshal(environmentOutput{
		EnvironmentId:      "env-1",
		EnvironmentVersion: "1.0.0",
		EnvironmentName:    "echo_env",
	})
	if err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["environmentName"] != "echo_env" {
		t.Fatalf("expected environmentName field, got %v", payload)
	}
	if _, exists := payload["name"]; exists {
		t.Fatalf("expected legacy name field to be omitted, got %v", payload)
	}
}
