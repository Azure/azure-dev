// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type extensionUpdateCheckerFunc func(context.Context, string) (*extensionUpdate, error)

func (f extensionUpdateCheckerFunc) Check(
	ctx context.Context,
	currentVersion string,
) (*extensionUpdate, error) {
	return f(ctx, currentVersion)
}

func TestRegistryBreakingUpdateCheckerRequiresUpdateAcrossBreakingVersion(t *testing.T) {
	server := newRegistryServer(t, `{
		"extensions": [{
			"id": "azure.ai.rle",
			"versions": [
				{"version": "0.8.5-preview"},
				{"version": "0.8.6-preview", "breakingChanges": true},
				{"version": "0.8.7-preview"}
			]
		}]
	}`)

	checker := &registryExtensionUpdateChecker{
		client:      server.Client(),
		registryURL: server.URL,
	}
	update, err := checker.Check(t.Context(), "0.8.5-preview")
	if err != nil {
		t.Fatal(err)
	}
	if update == nil || update.LatestVersion != "0.8.7-preview" {
		t.Fatalf("expected required update to 0.8.7-preview, got %#v", update)
	}
	if !update.IsBreaking {
		t.Fatalf("expected update to cross a breaking release, got %#v", update)
	}
}

func TestRegistryUpdateCheckerReportsNonBreakingUpdate(t *testing.T) {
	server := newRegistryServer(t, `{
		"extensions": [{
			"id": "azure.ai.rle",
			"versions": [
				{"version": "0.8.5-preview"},
				{"version": "0.8.6-preview"}
			]
		}]
	}`)

	checker := &registryExtensionUpdateChecker{
		client:      server.Client(),
		registryURL: server.URL,
	}
	update, err := checker.Check(t.Context(), "0.8.5-preview")
	if err != nil {
		t.Fatal(err)
	}
	if update == nil || update.LatestVersion != "0.8.6-preview" {
		t.Fatalf("expected available update to 0.8.6-preview, got %#v", update)
	}
	if update.IsBreaking {
		t.Fatalf("expected non-breaking update, got %#v", update)
	}
}

func TestRegistryBreakingUpdateCheckerSkipsDevelopmentBuild(t *testing.T) {
	checker := &registryExtensionUpdateChecker{
		client:      http.DefaultClient,
		registryURL: "://invalid",
	}
	update, err := checker.Check(t.Context(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if update != nil {
		t.Fatalf("expected development build not to check for updates, got %#v", update)
	}
}

func TestRootCommandBlocksNormalCommandForBreakingUpdate(t *testing.T) {
	oldVersion := Version
	Version = "0.8.5-preview"
	t.Cleanup(func() {
		Version = oldVersion
	})

	checker := extensionUpdateCheckerFunc(func(context.Context, string) (*extensionUpdate, error) {
		return &extensionUpdate{LatestVersion: "0.8.6-preview", IsBreaking: true}, nil
	})
	rootCmd := newRootCommand(checker)
	ran := false
	rootCmd.AddCommand(&cobra.Command{
		Use: "probe",
		Run: func(cmd *cobra.Command, args []string) {
			ran = true
		},
	})
	rootCmd.SetArgs([]string{"probe"})

	err := rootCmd.Execute()
	localError, ok := errors.AsType[*azdext.LocalError](err)
	if !ok || localError.Code != "rle_breaking_update_required" {
		t.Fatalf("expected breaking update error, got %v", err)
	}
	if ran {
		t.Fatal("expected command not to run before the required update")
	}
	if !strings.Contains(localError.Suggestion, "azd extension update azure.ai.rle") {
		t.Fatalf("expected actionable update suggestion, got %q", localError.Suggestion)
	}
}

func TestRootCommandNotifiesAndRunsForNonBreakingUpdate(t *testing.T) {
	oldVersion := Version
	Version = "0.8.5-preview"
	t.Cleanup(func() {
		Version = oldVersion
	})

	checker := extensionUpdateCheckerFunc(func(context.Context, string) (*extensionUpdate, error) {
		return &extensionUpdate{LatestVersion: "0.8.6-preview"}, nil
	})
	rootCmd := newRootCommand(checker)
	var output bytes.Buffer
	rootCmd.SetOut(&output)
	rootCmd.SetErr(&output)
	ran := false
	rootCmd.AddCommand(&cobra.Command{
		Use: "probe",
		Run: func(cmd *cobra.Command, args []string) {
			ran = true
			fmt.Fprintln(cmd.OutOrStdout(), "command output")
		},
	})
	rootCmd.SetArgs([]string{"probe"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("expected command to run for a non-breaking update")
	}
	if !strings.Contains(output.String(), "RLE extension update available: 0.8.6-preview") {
		t.Fatalf("expected update notice, got %q", output.String())
	}
	if !strings.Contains(output.String(), "azd extension update azure.ai.rle") {
		t.Fatalf("expected actionable update command, got %q", output.String())
	}
	if strings.Index(output.String(), "command output") > strings.Index(output.String(), "RLE extension update available") {
		t.Fatalf("expected update notice after command output, got %q", output.String())
	}
}

func TestRootCommandAllowsCommandWhenRegistryCheckFails(t *testing.T) {
	checker := extensionUpdateCheckerFunc(func(context.Context, string) (*extensionUpdate, error) {
		return nil, errors.New("registry unavailable")
	})
	rootCmd := newRootCommand(checker)
	var stderr bytes.Buffer
	rootCmd.SetErr(&stderr)
	ran := false
	rootCmd.AddCommand(&cobra.Command{
		Use: "probe",
		Run: func(cmd *cobra.Command, args []string) {
			ran = true
		},
	})
	rootCmd.SetArgs([]string{"probe"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("expected command to run when the registry check is unavailable")
	}
	if !strings.Contains(stderr.String(), "unable to check for RLE updates") {
		t.Fatalf("expected registry warning, got %q", stderr.String())
	}
}

func TestRootCommandDoesNotCheckRecoveryCommands(t *testing.T) {
	for _, commandName := range []string{"metadata", "version"} {
		t.Run(commandName, func(t *testing.T) {
			checks := 0
			checker := extensionUpdateCheckerFunc(func(context.Context, string) (*extensionUpdate, error) {
				checks++
				return &extensionUpdate{LatestVersion: "0.8.6-preview", IsBreaking: true}, nil
			})
			rootCmd := newRootCommand(checker)
			rootCmd.SetOut(&bytes.Buffer{})
			rootCmd.SetArgs([]string{commandName})

			if err := rootCmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if checks != 0 {
				t.Fatalf("expected %s command to bypass update check, got %d checks", commandName, checks)
			}
		})
	}
}

func newRegistryServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}
