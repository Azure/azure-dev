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

func TestInitStructuredErrors(t *testing.T) {
	for _, tt := range []struct {
		name     string
		fixture  string
		args     []string
		code     string
		decline  bool
		category azdext.LocalErrorCategory
	}{
		{"consent", "three-engines", []string{"--no-prompt"},
			codeConsentRequired, false, azdext.LocalErrorCategoryValidation},
		{"environment", "three-engines", []string{"--yes", "--environment="},
			codeConflictingArguments, false, azdext.LocalErrorCategoryValidation},
		{"output", "three-engines", []string{"--yes", "--output=json"},
			codeConflictingArguments, false, azdext.LocalErrorCategoryValidation},
		{"profiles", "three-engines", []string{"--yes", "--profiles="},
			codeInvalidProfiles, false, azdext.LocalErrorCategoryValidation},
		{"invalid", "driver-conflict", []string{"--yes"},
			codeInvalidConfiguration, false, azdext.LocalErrorCategoryValidation},
		{"unsupported", "file-storage", []string{"--yes"},
			codeUnsupportedConfiguration, false, azdext.LocalErrorCategoryValidation},
		{"unresolved", "missing-url", []string{"--yes", "--no-prompt"},
			codeUnresolvedDecision, false, azdext.LocalErrorCategoryValidation},
		{"empty answer", "missing-url", []string{"--yes"},
			codeUnresolvedDecision, false, azdext.LocalErrorCategoryValidation},
		{"declined trust", "three-engines", nil,
			codeCancelled, true, azdext.LocalErrorCategoryUser},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command, _, _ := fixtureCommand(t, tt.fixture, func() (initPrompter, error) {
				return &fakePrompter{decline: tt.decline}, nil
			})
			command.SetArgs(append([]string{"init"}, tt.args...))
			err := command.ExecuteContext(t.Context())
			var local *azdext.LocalError
			require.ErrorAs(t, err, &local)
			require.Equal(t, tt.code, local.Code)
			require.Equal(t, tt.category, local.Category)
			if tt.category == azdext.LocalErrorCategoryValidation {
				require.NotEmpty(t, local.Suggestion)
			}
			report := azdext.WrapError(err)
			require.NotNil(t, report.GetLocalError())
			require.Equal(t, tt.code, report.GetLocalError().GetCode())
			require.Equal(t, string(tt.category), report.GetLocalError().GetCategory())
			_, statErr := os.Stat("azure.yaml")
			require.ErrorIs(t, statErr, fs.ErrNotExist)
		})
	}
}

type fixtureResolver struct{ calls int }

func (r *fixtureResolver) Resolve(_ context.Context, directory string) (*spring.ModelData, error) {
	r.calls++
	var escaped strings.Builder
	if err := xml.EscapeText(&escaped, []byte(filepath.Join(directory, "target"))); err != nil {
		return nil, err
	}
	return &spring.ModelData{EffectivePom: fmt.Appendf(nil, `<project><artifactId>inventory</artifactId>
<properties><java.version>17</java.version></properties>
<build><directory>%s</directory><finalName>inventory</finalName><plugins><plugin>
<groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId>
<executions><execution><goals><goal>repackage</goal></goals></execution></executions>
</plugin></plugins></build><dependencies>
<dependency><groupId>com.h2database</groupId><artifactId>h2</artifactId></dependency>
<dependency><groupId>com.mysql</groupId><artifactId>mysql-connector-j</artifactId></dependency>
<dependency><groupId>org.postgresql</groupId><artifactId>postgresql</artifactId></dependency>
</dependencies></project>`, escaped.String())}, nil
}

type fakePrompter struct {
	confirmations []string
	decisions     []spring.Decision
	answer        string
	answers       map[string]string
	decline       bool
	declinePlan   bool
	declineAt     int
	err           error
	closed        bool
}

func (p *fakePrompter) Confirm(_ context.Context, message string) (bool, error) {
	p.confirmations = append(p.confirmations, message)
	return !p.decline && !(p.declinePlan && len(p.confirmations) == 2) &&
		len(p.confirmations) != p.declineAt, p.err
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

func TestInitDatabaseNetworkDescription(t *testing.T) {
	for _, profile := range []string{"h2", "mysql", "postgres"} {
		t.Run(profile, func(t *testing.T) {
			command, _, output := fixtureCommand(t, "three-engines", func() (initPrompter, error) {
				t.Fatal("resolved --yes invocation must not open a prompt channel")
				return nil, errors.New("unavailable")
			})
			command.SetArgs([]string{"init", "--yes", "--no-prompt", "--profiles", profile})
			require.NoError(t, command.ExecuteContext(t.Context()))
			const message = "Database network: public endpoint restricted to reported Container Apps outbound IPs."
			if profile == "h2" {
				require.NotContains(t, output.String(), message)
			} else {
				require.Contains(t, output.String(), message)
			}
			require.NotContains(t, output.String(), "allows all public IP addresses")
			init, _, err := command.Find([]string{"init"})
			require.NoError(t, err)
			require.Contains(t, init.Long, "Container Apps outbound IPs")
			require.NotContains(t, init.Long, "broad development")
		})
	}
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

func TestInitRejectsExistingDeploymentFilesBeforeMaven(t *testing.T) {
	for _, tt := range []struct {
		name    string
		content string
	}{
		{"infra", ""},
		{"azure.yml", "name: existing\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command, resolver, _ := fixtureCommand(t, "three-engines", func() (initPrompter, error) {
				t.Fatal("existing deployment files must be rejected before trust prompts")
				return nil, errors.New("unavailable")
			})
			if tt.name == "infra" {
				require.NoError(t, os.Mkdir(tt.name, 0o750))
			} else {
				require.NoError(t, os.WriteFile(tt.name, []byte(tt.content), 0o600))
			}
			command.SetArgs([]string{"init"})
			require.ErrorContains(t, command.ExecuteContext(t.Context()), tt.name+" already exists")
			require.Zero(t, resolver.calls)
			if tt.name != "infra" {
				data, err := os.ReadFile(tt.name)
				require.NoError(t, err)
				require.Equal(t, tt.content, string(data))
			}
			_, statErr := os.Stat("azure.yaml")
			require.ErrorIs(t, statErr, fs.ErrNotExist)
		})
	}
}

func TestInitRejectsRootDockerignoreWithoutDockerfileBeforeMaven(t *testing.T) {
	for _, args := range [][]string{{"init"}, {"init", "--yes", "--no-prompt"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			command, resolver, _ := fixtureCommand(t, "three-engines", func() (initPrompter, error) {
				t.Fatal("incompatible Docker inputs must be rejected before prompts")
				return nil, errors.New("unavailable")
			})
			require.NoError(t, os.WriteFile(".dockerignore", []byte("private/\n"), 0o600))
			command.SetArgs(args)
			require.ErrorContains(t, command.ExecuteContext(t.Context()),
				".dockerignore requires an application-owned root Dockerfile")
			require.Zero(t, resolver.calls)
			for _, name := range []string{"azure.yaml", "infra", ".azd-spring"} {
				_, err := os.Stat(name)
				require.ErrorIs(t, err, fs.ErrNotExist)
			}
			data, err := os.ReadFile(".dockerignore")
			require.NoError(t, err)
			require.Equal(t, "private/\n", string(data))
		})
	}
}

func TestInitUsesExistingDockerfileWithoutModification(t *testing.T) {
	command, _, output := fixtureCommand(t, "three-engines", func() (initPrompter, error) {
		t.Fatal("resolved no-prompt initialization must not open prompts")
		return nil, errors.New("unavailable")
	})
	const dockerfile = "FROM eclipse-temurin:17-jre\nENTRYPOINT [\"java\", \"-jar\", \"/app/app.jar\"]\n"
	require.NoError(t, os.WriteFile("Dockerfile", []byte(dockerfile), 0o600))
	require.NoError(t, os.WriteFile(".dockerignore", []byte("private/\n"), 0o600))
	command.SetArgs([]string{"init", "--yes", "--no-prompt", "--profiles", "mysql"})
	require.NoError(t, command.ExecuteContext(t.Context()))
	require.Contains(t, output.String(), "application-owned Dockerfile")
	require.Contains(t, output.String(), "Database: mysql server")
	data, err := os.ReadFile("Dockerfile")
	require.NoError(t, err)
	require.Equal(t, dockerfile, string(data))
	data, err = os.ReadFile(".dockerignore")
	require.NoError(t, err)
	require.Equal(t, "private/\n", string(data))
	_, err = os.Stat(filepath.Join(".azd-spring", "Dockerfile"))
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = os.Stat(filepath.Join(".azd-spring", "Dockerfile.dockerignore"))
	require.ErrorIs(t, err, fs.ErrNotExist)
	_, err = os.Stat(filepath.Join(".azd-spring", "application-azd.properties"))
	require.ErrorIs(t, err, fs.ErrNotExist)
	parameters, err := os.ReadFile(filepath.Join("infra", "application.parameters.json"))
	require.NoError(t, err)
	require.Contains(t, string(parameters), `"environmentBindings":`)
	require.Contains(t, string(parameters), `"jdbcOptions":`)
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

func TestInitMaterializesPromptDerivedDriver(t *testing.T) {
	prompts := &fakePrompter{answer: "postgresql"}
	command, resolver, _ := fixtureCommand(t, "prompt-driver", func() (initPrompter, error) {
		return prompts, nil
	})
	command.SetArgs([]string{"init", "--yes"})
	require.NoError(t, command.ExecuteContext(t.Context()))
	require.Len(t, prompts.decisions, 1)
	require.Equal(t, "DB_ENGINE", prompts.decisions[0].Input)
	require.Equal(t, 1, resolver.calls)
	properties, err := os.ReadFile(filepath.Join(".azd-spring", "application-azd.properties"))
	require.NoError(t, err)
	require.Contains(t, string(properties), "spring.datasource.driver-class-name=org.postgresql.Driver\n")
	require.NotContains(t, string(properties), "${DB_ENGINE}")
	original, err := os.ReadFile(filepath.Join("src", "main", "resources", "application.properties"))
	require.NoError(t, err)
	require.Contains(t, string(original), "spring.datasource.driver-class-name=org.${DB_ENGINE}.Driver")
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
	require.Nil(t, initCommand.Flags().Lookup("database-auth"))
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

func TestInitRepairsDatasourcePasswordInteractively(t *testing.T) {
	const secret = "local-default-must-not-be-disclosed"
	prompts := &fakePrompter{}
	command, resolver, output := fixtureCommand(t, "three-engines", func() (initPrompter, error) {
		return prompts, nil
	})
	file := filepath.Join("src", "main", "resources", "application-mysql.properties")
	require.NoError(t, os.WriteFile(file,
		[]byte("spring.datasource.url=jdbc:mysql://localhost/catalog\n"+
			"spring.datasource.password=${MYSQL_PASSWORD:"+secret+"}\n"), 0o600))
	command.SetArgs([]string{"init", "--yes", "--profiles", "mysql"})
	require.NoError(t, command.ExecuteContext(t.Context()))
	require.Equal(t, 1, resolver.calls)
	require.Len(t, prompts.confirmations, 1)
	require.Contains(t, prompts.confirmations[0], "spring.datasource.password")
	require.NotContains(t, prompts.confirmations[0], secret)
	require.NotContains(t, output.String(), secret)
	data, err := fs.ReadFile(os.DirFS("."), filepath.ToSlash(file))
	require.NoError(t, err)
	require.Contains(t, string(data), "spring.datasource.password=${MYSQL_PASSWORD}\n")
	require.NotContains(t, string(data), secret)
}

func TestInitNoPromptDoesNotRepairDatasourcePassword(t *testing.T) {
	const original = "spring.datasource.password=${DATABASE_PASSWORD:local-default}\n"
	command, _, _ := fixtureCommand(t, "three-engines", func() (initPrompter, error) {
		t.Fatal("no-prompt mode must not offer source repairs")
		return nil, errors.New("unavailable")
	})
	file := filepath.Join("src", "main", "resources", "application.properties")
	require.NoError(t, os.WriteFile(file, []byte(original), 0o600))
	command.SetArgs([]string{"init", "--yes", "--no-prompt"})
	require.ErrorContains(t, command.ExecuteContext(t.Context()), "runtime placeholder without a default")
	data, err := fs.ReadFile(os.DirFS("."), filepath.ToSlash(file))
	require.NoError(t, err)
	require.Equal(t, original, string(data))
}

func TestInitRepairsPackagedAndExternalPasswords(t *testing.T) {
	for _, tt := range []struct {
		name      string
		noPrompt  bool
		declineAt int
	}{
		{name: "confirm both batches"},
		{name: "decline external batch", declineAt: 2},
		{name: "no prompt", noPrompt: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prompts := &fakePrompter{declineAt: tt.declineAt}
			command, resolver, output := fixtureCommand(t, "three-engines", func() (initPrompter, error) {
				require.False(t, tt.noPrompt, "no-prompt must not request repair consent")
				return prompts, nil
			})
			files := []string{
				filepath.Join("src", "main", "resources", "application-mysql.properties"),
				filepath.Join("config", "application-mysql.properties"),
			}
			const testValue = "repair-fixture-value"
			original := "spring.datasource.url=jdbc:mysql://localhost/catalog\n" +
				"spring.datasource.password=${MYSQL_PASSWORD:" + testValue + "}\n"
			for _, file := range files {
				require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o750))
				require.NoError(t, os.WriteFile(file, []byte(original), 0o600))
			}
			args := []string{"init", "--yes", "--profiles", "mysql"}
			if tt.noPrompt {
				args = append(args, "--no-prompt")
			}
			command.SetArgs(args)
			err := command.ExecuteContext(t.Context())
			if tt.noPrompt {
				require.ErrorContains(t, err, "runtime placeholder without a default")
				require.Empty(t, prompts.confirmations)
			} else {
				require.Len(t, prompts.confirmations, 2, "--yes must still confirm each newly discovered repair batch")
				for index, message := range prompts.confirmations {
					require.Contains(t, message, filepath.ToSlash(files[index]))
					require.Contains(t, message, "spring.datasource.password")
					require.NotContains(t, message, testValue)
				}
				if tt.declineAt != 0 {
					require.ErrorContains(t, err, "cancelled")
				} else {
					require.NoError(t, err)
				}
			}
			require.Equal(t, 1, resolver.calls, "repair passes must reuse Maven inspection")
			require.NotContains(t, output.String(), testValue)
			if err != nil {
				require.NotContains(t, err.Error(), testValue)
				_, statErr := os.Stat("azure.yaml")
				require.ErrorIs(t, statErr, fs.ErrNotExist)
			}
			for index, file := range files {
				data, readErr := fs.ReadFile(os.DirFS("."), filepath.ToSlash(file))
				require.NoError(t, readErr)
				if tt.noPrompt || (index == 1 && tt.declineAt != 0) {
					require.Equal(t, original, string(data), "unconfirmed files must remain unchanged")
				} else {
					require.NotContains(t, string(data), testValue)
					require.Contains(t, string(data), "spring.datasource.password=${MYSQL_PASSWORD}")
				}
			}
		})
	}
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
