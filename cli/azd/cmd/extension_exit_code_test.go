// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	cmdinternal "github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mocktracing"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestExtensionAction_Run_ExitCode(t *testing.T) {
	originalArgs := os.Args
	os.Args = []string{"azd", "--output=json"}
	t.Cleanup(func() { os.Args = originalArgs })

	processErr := errors.New("process failed")
	localErr := &azdext.LocalError{
		Message:    "quality gate not met",
		Code:       "quality_gate",
		Category:   azdext.LocalErrorCategoryValidation,
		Suggestion: "Inspect the evaluation results",
	}
	serviceErr := &azdext.ServiceError{
		Message:     "service unavailable",
		ErrorCode:   "Unavailable",
		StatusCode:  503,
		ServiceName: "test.service",
	}

	tests := []struct {
		name           string
		exitCode       int
		runErr         error
		reportedErr    error
		wantExitCode   bool
		wantTelemetry  string
		missingProgram bool
	}{
		{name: "Success"},
		{name: "Failure", exitCode: 1, runErr: processErr, wantExitCode: true, wantTelemetry: "ext.run.failed"},
		{name: "QualityGate", exitCode: 2, runErr: processErr, wantExitCode: true, wantTelemetry: "ext.run.failed"},
		{name: "OtherExitCode", exitCode: 42, runErr: processErr, wantExitCode: true, wantTelemetry: "ext.run.failed"},
		{
			name: "WrappedFailure", exitCode: 2, runErr: fmt.Errorf("waiting: %w", processErr),
			wantExitCode: true, wantTelemetry: "ext.run.failed",
		},
		{
			name: "ReportedLocalError", exitCode: 2, runErr: processErr, reportedErr: localErr,
			wantExitCode: true, wantTelemetry: "ext.validation.quality_gate",
		},
		{
			name: "ReportedServiceError", exitCode: 2, runErr: processErr, reportedErr: serviceErr,
			wantExitCode: true, wantTelemetry: "ext.service.unavailable",
		},
		{name: "LaunchFailure", runErr: processErr, wantTelemetry: "ext.run.failed"},
		{name: "NoProcessExitCode", exitCode: -1, runErr: processErr, wantTelemetry: "ext.run.failed"},
		{name: "MissingProgram", missingProgram: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configDir := t.TempDir()
			t.Setenv("AZD_CONFIG_DIR", configDir)
			extensionPath := filepath.Join("extensions", "test-ext", "test-ext")
			if !tt.missingProgram {
				fullPath := filepath.Join(configDir, extensionPath)
				require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
				require.NoError(t, os.WriteFile(fullPath, []byte("test"), 0o600))
			}

			mockCtx := mocks.NewMockContext(t.Context())
			extension := &extensions.Extension{Id: "test-ext", Path: extensionPath, Version: "1.0.0"}
			manager := newExtensionActionTestManager(t, mockCtx, extension)
			installed, err := manager.GetInstalled(extensions.FilterOptions{Id: extension.Id})
			require.NoError(t, err)
			mockCtx.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
				return args.Cmd == filepath.Join(configDir, extensionPath)
			}).RespondFn(func(exec.RunArgs) (exec.RunResult, error) {
				if tt.reportedErr != nil {
					installed.SetReportedError(tt.reportedErr)
				}
				return exec.NewRunResult(tt.exitCode, "", ""), tt.runErr
			})

			action := &extensionAction{
				console:          mockCtx.Console,
				extensionRunner:  extensions.NewRunner(mockCtx.CommandRunner),
				lazyEnv:          lazy.From[environment.Env](nil),
				extensionManager: manager,
				azdServer:        newExtensionActionTestServer(),
				globalOptions:    &internal.GlobalCommandOptions{},
				cmd:              &cobra.Command{Annotations: map[string]string{"extension.id": extension.Id}},
			}
			result, err := action.Run(t.Context())
			require.Nil(t, result)
			if tt.runErr == nil && !tt.missingProgram {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			exitErr, hasExitCode := errors.AsType[*internal.ExitCodeError](err)
			require.Equal(t, tt.wantExitCode, hasExitCode)
			if hasExitCode {
				require.Equal(t, tt.exitCode, exitErr.ExitCode)
			}
			if tt.missingProgram {
				require.ErrorContains(t, err, "not found")
				return
			}

			require.ErrorIs(t, err, processErr)
			runErr, ok := errors.AsType[*extensions.ExtensionRunError](err)
			require.True(t, ok)
			require.Equal(t, extension.Id, runErr.ExtensionId)
			require.Equal(t, extension.Version, runErr.ExtensionVersion)
			if tt.reportedErr != nil {
				require.ErrorIs(t, err, tt.reportedErr)
				require.Equal(t, azdext.ErrorSuggestion(tt.reportedErr), azdext.ErrorSuggestion(err))
			}
			span := &mocktracing.Span{}
			cmdinternal.MapError(err, span)
			require.Equal(t, tt.wantTelemetry, span.Status.Description)
		})
	}
}
