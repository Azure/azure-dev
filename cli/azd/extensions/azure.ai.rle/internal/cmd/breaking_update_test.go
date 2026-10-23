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
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type extensionUpdateCheckerFunc func(context.Context, string) (*extensionUpdate, error)

func (f extensionUpdateCheckerFunc) Check(
	ctx context.Context,
	currentVersion string,
) (*extensionUpdate, error) {
	return f(ctx, currentVersion)
}

func TestRegistryExtensionUpdateCheckerRequiresInstalledSource(t *testing.T) {
	checker := &registryExtensionUpdateChecker{
		resolveSource: func() (extensionUpdateSource, error) {
			return extensionUpdateSource{}, errors.New("source is missing")
		},
	}
	if update, err := checker.Check(t.Context(), "0.8.5-preview"); err == nil || update != nil {
		t.Fatalf("expected missing release registry error, got update=%#v, err=%v", update, err)
	}
	if update, err := checker.Check(t.Context(), "dev"); err != nil || update != nil {
		t.Fatalf("expected unversioned build to skip update checks, got update=%#v, err=%v", update, err)
	}
}

func TestUpdateMessagesUseInstalledSource(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		command string
	}{
		{"customer", "rle", "azd extension update azure.ai.rle --source rle"},
		{"development", "rle-dev", "azd extension update azure.ai.rle --source rle-dev"},
		{"custom", "team.preview", "azd extension update azure.ai.rle --source team.preview"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extensionUpdateCommand(tt.source); got != tt.command {
				t.Fatalf("expected update command %q, got %q", tt.command, got)
			}
			update := &extensionUpdate{LatestVersion: "0.8.6-preview", IsBreaking: true, Source: tt.source}
			localError, ok := errors.AsType[*azdext.LocalError](breakingUpdateError(update))
			if !ok || localError.Suggestion != "Run: "+tt.command {
				t.Fatalf("expected channel-specific breaking suggestion, got %#v", localError)
			}

			checker := extensionUpdateCheckerFunc(func(context.Context, string) (*extensionUpdate, error) {
				return &extensionUpdate{LatestVersion: "0.8.6-preview", Source: tt.source}, nil
			})
			rootCmd := newRootCommand(checker)
			var output bytes.Buffer
			rootCmd.SetOut(&output)
			rootCmd.SetErr(&output)
			rootCmd.AddCommand(&cobra.Command{
				Use: "probe",
				Run: func(cmd *cobra.Command, args []string) {},
			})
			rootCmd.SetArgs([]string{"probe"})
			if err := rootCmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "To update, run `"+tt.command+"`") {
				t.Fatalf("expected channel-specific non-breaking notice, got %q", output.String())
			}
		})
	}
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
		client: server.Client(),
		resolveSource: func() (extensionUpdateSource, error) {
			return extensionUpdateSource{Name: "rle", Type: "url", Location: server.URL}, nil
		},
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
		client: server.Client(),
		resolveSource: func() (extensionUpdateSource, error) {
			return extensionUpdateSource{Name: "rle-dev", Type: "url", Location: server.URL}, nil
		},
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
		client: http.DefaultClient,
		resolveSource: func() (extensionUpdateSource, error) {
			t.Fatal("development build attempted source resolution")
			return extensionUpdateSource{}, nil
		},
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
		return &extensionUpdate{LatestVersion: "0.8.6-preview", IsBreaking: true, Source: "rle"}, nil
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
	if localError.Message != "Update the RLE extension to 0.8.6-preview before continuing." {
		t.Fatalf("unexpected blocking message: %q", localError.Message)
	}
	if localError.Suggestion != "Run: "+extensionUpdateCommand("rle") {
		t.Fatalf("unexpected update suggestion: %q", localError.Suggestion)
	}
}

func TestRootCommandNotifiesAndRunsForNonBreakingUpdate(t *testing.T) {
	previousNoColor := color.NoColor
	color.NoColor = false
	t.Cleanup(func() {
		color.NoColor = previousNoColor
	})

	oldVersion := Version
	Version = "0.8.5-preview"
	t.Cleanup(func() {
		Version = oldVersion
	})

	checker := extensionUpdateCheckerFunc(func(context.Context, string) (*extensionUpdate, error) {
		return &extensionUpdate{LatestVersion: "0.8.6-preview", Source: "rle"}, nil
	})
	rootCmd := newRootCommand(checker)
	var output bytes.Buffer
	var stderr bytes.Buffer
	rootCmd.SetOut(&output)
	rootCmd.SetErr(&stderr)
	ran := false
	rootCmd.AddCommand(&cobra.Command{
		Use: "probe",
		Run: func(cmd *cobra.Command, args []string) {
			ran = true
			if stderr.Len() != 0 {
				t.Fatal("expected the update notice only after command execution")
			}
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
	expectedNotice := "RLE extension update available: 0.8.6-preview\n" +
		"To update, run `" + extensionUpdateCommand("rle") + "`\n\n"
	plainOutput := strings.ReplaceAll(stderr.String(), "\x1b[33m", "")
	plainOutput = strings.ReplaceAll(plainOutput, "\x1b[0m", "")
	if !strings.Contains(plainOutput, expectedNotice) {
		t.Fatalf("expected update notice on stderr, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "\x1b[33m") {
		t.Fatalf("expected yellow update notice, got %q", stderr.String())
	}
	if output.String() != "command output\n" {
		t.Fatalf("expected only command output on stdout, got %q", output.String())
	}
}

func TestRootCommandWarnsAndAllowsCommandWhenRegistryCheckFails(t *testing.T) {
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
	if !strings.Contains(stderr.String(), "Warning: unable to check for RLE updates: registry unavailable") {
		t.Fatalf("expected explicit registry warning, got %q", stderr.String())
	}
}

func TestRootCommandReportsRegistryCheckFailureInDebugMode(t *testing.T) {
	checker := extensionUpdateCheckerFunc(func(context.Context, string) (*extensionUpdate, error) {
		return nil, errors.New("registry unavailable")
	})
	rootCmd := newRootCommand(checker)
	var stderr bytes.Buffer
	rootCmd.SetErr(&stderr)
	rootCmd.AddCommand(&cobra.Command{Use: "probe", Run: func(cmd *cobra.Command, args []string) {}})
	rootCmd.SetArgs([]string{"--debug", "probe"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "Warning: unable to check for RLE updates: registry unavailable") {
		t.Fatalf("expected registry diagnostic in debug mode, got %q", stderr.String())
	}
}

func TestRootCommandDoesNotCheckRecoveryCommands(t *testing.T) {
	for _, commandName := range []string{"metadata", "models", "version"} {
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
