// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"azure.springboot/internal/spring"
)

func TestInitPreflight(t *testing.T) {
	for _, tt := range []struct {
		name      string
		args      []string
		input     string
		errorText string
	}{
		{"consent in scripts", []string{"init", "--no-prompt"}, "", "requires --yes"},
		{"ignored environment", []string{"init", "--yes", "--environment", "release"}, "", "--environment"},
		{"ignored output format", []string{"init", "--yes", "--output", "json"}, "", "--output"},
		{"no positional project", []string{"init", "somewhere"}, "", "unknown command"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("AZD_NO_PROMPT", "")
			t.Setenv("AZD_CWD", "")
			t.Setenv("AZD_ENVIRONMENT", "")
			command := NewRootCommand()
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetIn(strings.NewReader(tt.input))
			command.SetArgs(tt.args)
			err := command.ExecuteContext(t.Context())
			require.ErrorContains(t, err, tt.errorText)
			entries, err := os.ReadDir(".")
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

type fixtureResolver struct{ calls int }

func (r *fixtureResolver) Resolve(_ context.Context, directory string) ([]byte, error) {
	r.calls++
	var escaped strings.Builder
	if err := xml.EscapeText(&escaped, []byte(filepath.Join(directory, "target"))); err != nil {
		return nil, err
	}
	return fmt.Appendf(nil, `<project><artifactId>inventory</artifactId>
<properties><java.version>17</java.version></properties>
<build><directory>%s</directory><finalName>inventory</finalName><plugins><plugin>
<groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId>
<executions><execution><goals><goal>repackage</goal></goals></execution></executions>
</plugin></plugins></build><dependencies>
<dependency><groupId>com.h2database</groupId><artifactId>h2</artifactId></dependency>
<dependency><groupId>com.mysql</groupId><artifactId>mysql-connector-j</artifactId></dependency>
<dependency><groupId>org.postgresql</groupId><artifactId>postgresql</artifactId></dependency>
</dependencies></project>`, escaped.String()), nil
}

type fakePrompter struct {
	confirmations []string
	decisions     []spring.Decision
	answer        string
	answers       map[string]string
	decline       bool
	declinePlan   bool
	err           error
	closed        bool
}

func (p *fakePrompter) Confirm(_ context.Context, message string) (bool, error) {
	p.confirmations = append(p.confirmations, message)
	return !p.decline && !(p.declinePlan && len(p.confirmations) == 2), p.err
}

func (p *fakePrompter) Decide(_ context.Context, decision spring.Decision) (string, error) {
	p.decisions = append(p.decisions, decision)
	if answer, ok := p.answers[decision.ID]; ok {
		return answer, p.err
	}
	return p.answer, p.err
}

func (p *fakePrompter) Close() { p.closed = true }

func fixtureCommand(
	t *testing.T, fixture string, openPrompts func() (initPrompter, error),
) (*cobra.Command, *fixtureResolver, *bytes.Buffer) {
	t.Helper()
	directory := t.TempDir()
	source := os.DirFS(filepath.Join("..", "spring", "testdata", "resolution", fixture))
	require.NoError(t, fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(source, name)
		if err != nil {
			return err
		}
		destination := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o600)
	}))
	require.NoError(t, os.WriteFile(filepath.Join(directory, "pom.xml"), []byte("<project/>"), 0o600))
	t.Chdir(directory)
	for _, name := range []string{"AZD_NO_PROMPT", "AZD_CWD", "AZD_ENVIRONMENT"} {
		t.Setenv(name, "")
	}
	root, extCtx := azdext.NewExtensionRootCommand(azdext.ExtensionCommandOptions{Name: "spring"})
	root.SilenceUsage, root.SilenceErrors = true, true
	resolver := &fixtureResolver{}
	root.AddCommand(newInitCommandWithServices(extCtx, resolver, openPrompts))
	output := &bytes.Buffer{}
	root.SetOut(output)
	root.SetErr(output)
	return root, resolver, output
}

func TestInitResolutionFixtures(t *testing.T) {
	for _, tt := range []struct {
		name          string
		fixture       string
		args          []string
		answer        string
		decisions     int
		confirmations int
		errorText     string
	}{
		{"default H2", "three-engines", []string{"--yes", "--no-prompt"}, "", 0, 0, ""},
		{"default PostgreSQL", "default-postgres", []string{"--yes"}, "", 0, 0, ""},
		{"only normal confirmations", "h2-mysql", nil, "", 0, 2, ""},
		{"explicit profile", "profile-choice", []string{"--yes", "--no-prompt", "--profiles", "mysql"}, "", 0, 0, ""},
		{"yes does not invent answers", "profile-choice", []string{"--yes"}, "postgres", 1, 0, ""},
		{"default profile selector", "default-profile-choice", []string{"--yes"}, "h2", 1, 0, ""},
		{"missing URL", "missing-url", []string{"--yes"}, "jdbc:h2:mem:catalog", 1, 0, ""},
		{"missing port", "missing-port", []string{"--yes"}, "9090", 1, 0, ""},
		{"unanswered profile", "profile-choice", []string{"--yes", "--no-prompt"}, "", 0, 0, "--profiles"},
		{"unanswered URL", "missing-url", []string{"--yes", "--no-prompt"}, "", 0, 0, "configure DEPLOYMENT_JDBC_URL"},
		{"conflict is not a question", "driver-conflict", []string{"--yes"}, "", 0, 0, "invalid configuration"},
		{"unsupported is not a question", "file-storage", []string{"--yes"}, "", 0, 0, "unsupported configuration"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prompts := &fakePrompter{answer: tt.answer}
			command, resolver, output := fixtureCommand(t, tt.fixture, func() (initPrompter, error) {
				return prompts, nil
			})
			command.SetArgs(append([]string{"init"}, tt.args...))
			err := command.ExecuteContext(t.Context())
			require.Len(t, prompts.decisions, tt.decisions)
			require.Len(t, prompts.confirmations, tt.confirmations)
			require.Equal(t, tt.decisions+tt.confirmations > 0, prompts.closed)
			require.Equal(t, 1, resolver.calls, "answers must not rerun project-controlled Maven code")
			if tt.errorText != "" {
				require.ErrorContains(t, err, tt.errorText)
				if tt.fixture == "missing-url" {
					require.Contains(t, err.Error(), "spring.datasource.url")
					require.Contains(t, err.Error(), "application.properties")
					require.NotContains(t, err.Error(), "--property")
				}
				_, statErr := os.Stat("azure.yaml")
				require.ErrorIs(t, statErr, fs.ErrNotExist)
				return
			}
			require.NoError(t, err)
			require.Contains(t, output.String(), "Initialization complete.")
			_, err = os.Stat("azure.yaml")
			require.NoError(t, err)
			require.NotContains(t, output.String(), "jdbc:")
		})
	}
}

func TestInitNoPromptChannelRequiredForResolvedConfiguration(t *testing.T) {
	command, _, _ := fixtureCommand(t, "three-engines", func() (initPrompter, error) {
		t.Fatal("resolved --yes invocation must not open a prompt channel")
		return nil, errors.New("unavailable")
	})
	command.SetArgs([]string{"init", "--yes"})
	require.NoError(t, command.ExecuteContext(t.Context()))
}

func TestInitMissingPromptChannel(t *testing.T) {
	command, _, _ := fixtureCommand(t, "profile-choice", func() (initPrompter, error) {
		return nil, errors.New("unavailable")
	})
	command.SetArgs([]string{"init", "--yes"})
	require.ErrorContains(t, command.ExecuteContext(t.Context()), "supply --profiles")
	_, err := os.Stat("azure.yaml")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestInitDeclinesTrustBeforeMaven(t *testing.T) {
	prompts := &fakePrompter{decline: true}
	command, resolver, _ := fixtureCommand(t, "three-engines", func() (initPrompter, error) {
		return prompts, nil
	})
	command.SetArgs([]string{"init"})
	require.ErrorContains(t, command.ExecuteContext(t.Context()), "cancelled")
	require.Zero(t, resolver.calls)
	require.True(t, prompts.closed)
}

func TestInitRejectsExistingDockerIgnoreBeforeMaven(t *testing.T) {
	command, resolver, _ := fixtureCommand(t, "three-engines", func() (initPrompter, error) {
		t.Fatal("existing deployment files must be rejected before trust prompts")
		return nil, errors.New("unavailable")
	})
	const content = "private-config/\n"
	require.NoError(t, os.WriteFile(".dockerignore", []byte(content), 0o600))
	command.SetArgs([]string{"init"})
	require.ErrorContains(t, command.ExecuteContext(t.Context()), ".dockerignore already exists")
	require.Zero(t, resolver.calls)
	data, err := os.ReadFile(".dockerignore")
	require.NoError(t, err)
	require.Equal(t, content, string(data))
	_, err = os.Stat("azure.yaml")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestInitDecisionCancellation(t *testing.T) {
	prompts := &fakePrompter{err: context.Canceled}
	command, _, _ := fixtureCommand(t, "profile-choice", func() (initPrompter, error) {
		return prompts, nil
	})
	command.SetArgs([]string{"init", "--yes"})
	require.ErrorIs(t, command.ExecuteContext(t.Context()), context.Canceled)
	_, err := os.Stat("azure.yaml")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestInitDependentDecisions(t *testing.T) {
	prompts := &fakePrompter{answers: map[string]string{
		"profiles":           "postgres",
		"property:HTTP_PORT": "9090",
		"property:JDBC_URL":  "jdbc:postgresql://localhost/catalog",
	}}
	command, resolver, output := fixtureCommand(t, "profile-choice", func() (initPrompter, error) {
		return prompts, nil
	})
	require.NoError(t, os.WriteFile(filepath.Join("src", "main", "resources", "application-postgres.properties"),
		[]byte("spring.datasource.url=${JDBC_URL}\nserver.port=${HTTP_PORT}"), 0o600))
	command.SetArgs([]string{"init", "--yes"})
	require.NoError(t, command.ExecuteContext(t.Context()))
	require.Len(t, prompts.decisions, 3)
	require.Equal(t, "profiles", prompts.decisions[0].ID)
	require.Equal(t, 1, resolver.calls)
	require.Contains(t, output.String(), "postgresql")
}

func TestInitDeclinesPlanWithoutWriting(t *testing.T) {
	prompts := &fakePrompter{declinePlan: true}
	command, _, _ := fixtureCommand(t, "three-engines", func() (initPrompter, error) {
		return prompts, nil
	})
	command.SetArgs([]string{"init"})
	require.ErrorContains(t, command.ExecuteContext(t.Context()), "cancelled")
	require.Len(t, prompts.confirmations, 2)
	_, err := os.Stat("azure.yaml")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestInitRejectsGenericPropertyOption(t *testing.T) {
	command, resolver, _ := fixtureCommand(t, "three-engines", func() (initPrompter, error) {
		t.Fatal("unknown flags must not open prompts")
		return nil, errors.New("unavailable")
	})
	initCommand, _, err := command.Find([]string{"init"})
	require.NoError(t, err)
	require.NotNil(t, initCommand.Flags().Lookup("profiles"))
	require.Nil(t, initCommand.Flags().Lookup("property"))
	command.SetArgs([]string{"init", "--yes", "--no-prompt", "--property", "server.port=9090"})
	require.ErrorContains(t, command.ExecuteContext(t.Context()), "unknown flag: --property")
	require.Zero(t, resolver.calls)
}

func TestInitConfiguredPlaceholderNeedsNoPrompt(t *testing.T) {
	command, _, _ := fixtureCommand(t, "missing-url", func() (initPrompter, error) {
		t.Fatal("project configuration resolves the datasource without prompts")
		return nil, errors.New("unavailable")
	})
	require.NoError(t, os.WriteFile(filepath.Join("src", "main", "resources", "application.properties"),
		[]byte("spring.datasource.url=${DEPLOYMENT_JDBC_URL}\nDEPLOYMENT_JDBC_URL=jdbc:h2:mem:catalog"), 0o600))
	command.SetArgs([]string{"init", "--yes", "--no-prompt"})
	require.NoError(t, command.ExecuteContext(t.Context()))
}

func TestInitRejectsEmptyProfileSelection(t *testing.T) {
	command, resolver, _ := fixtureCommand(t, "three-engines", func() (initPrompter, error) {
		t.Fatal("invalid profile flags must not open prompts")
		return nil, errors.New("unavailable")
	})
	command.SetArgs([]string{"init", "--yes", "--no-prompt", "--profiles="})
	require.ErrorContains(t, command.ExecuteContext(t.Context()), "--profiles requires")
	require.Zero(t, resolver.calls)
}
