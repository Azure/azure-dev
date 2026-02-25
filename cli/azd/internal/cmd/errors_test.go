// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mocktracing"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

func Test_MapError(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		wantErrReason  string
		wantErrDetails []attribute.KeyValue
	}{
		{
			name:          "WithNilError",
			err:           nil,
			wantErrReason: "internal.<nil>",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("<nil>"),
			},
		},
		{
			name:          "WithOtherError",
			err:           errors.New("something bad happened!"),
			wantErrReason: "internal.errors_errorString",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("*errors.errorString"),
			},
		},
		{
			name: "WithToolExitError",
			err: &exec.ExitError{
				Cmd:      "any",
				ExitCode: 51,
			},
			wantErrReason: "tool.any.failed",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ToolName.Key).String("any"),
				fields.ErrorKey(fields.ToolExitCode.Key).Int(51),
			},
		},
		{
			name: "WithArmDeploymentError",
			err: &azapi.AzureDeploymentError{
				Operation: azapi.DeploymentOperationDeploy,
				Details: &azapi.DeploymentErrorLine{
					Code: "",
					Inner: []*azapi.DeploymentErrorLine{
						{
							Code: "Conflict",
							Inner: []*azapi.DeploymentErrorLine{
								{Code: "OutOfCapacity"},
								{Code: "RegionOutOfCapacity"},
							},
						},
						{
							Code:  "PreconditionFailed",
							Inner: []*azapi.DeploymentErrorLine{},
						},
						{
							Code: "",
							Inner: []*azapi.DeploymentErrorLine{
								{
									Code: "ServiceUnavailable",
									Inner: []*azapi.DeploymentErrorLine{
										{Code: "UnknownError"},
									},
								},
							},
						},
					},
				},
			},
			wantErrReason: "service.arm.deployment.failed",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceName.Key).String("arm"),
				fields.ErrorKey(fields.ServiceErrorCode.Key).String(mustMarshalJson(
					[]map[string]interface{}{
						{
							string(fields.ErrCode.Key):  "Conflict,PreconditionFailed",
							string(fields.ErrFrame.Key): 0,
						},
						{
							string(fields.ErrCode.Key):  "OutOfCapacity,RegionOutOfCapacity",
							string(fields.ErrFrame.Key): 1,
						},
						{
							string(fields.ErrCode.Key):  "ServiceUnavailable",
							string(fields.ErrFrame.Key): 1,
						},
						{
							string(fields.ErrCode.Key):  "UnknownError",
							string(fields.ErrFrame.Key): 2,
						},
					})),
			},
		},
		{
			name: "WithArmValidationError",
			err: &azapi.AzureDeploymentError{
				Operation: azapi.DeploymentOperationValidate,
				Details: &azapi.DeploymentErrorLine{
					Code: "InvalidTemplate",
					Inner: []*azapi.DeploymentErrorLine{
						{Code: "TemplateValidationFailed"},
					},
				},
			},
			wantErrReason: "service.arm.validate.failed",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceName.Key).String("arm"),
				fields.ErrorKey(fields.ServiceErrorCode.Key).String(mustMarshalJson(
					[]map[string]interface{}{
						{
							string(fields.ErrCode.Key):  "InvalidTemplate",
							string(fields.ErrFrame.Key): 0,
						},
						{
							string(fields.ErrCode.Key):  "TemplateValidationFailed",
							string(fields.ErrFrame.Key): 1,
						},
					})),
			},
		},
		{
			name: "WithResponseError",
			err: &azcore.ResponseError{
				ErrorCode:  "ServiceUnavailable",
				StatusCode: 503,
				RawResponse: &http.Response{
					StatusCode: 503,
					Request: &http.Request{
						Method: "GET",
						Host:   "management.azure.com",
					},
				},
			},
			wantErrReason: "service.arm.503",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceName.Key).String("arm"),
				fields.ErrorKey(fields.ServiceHost.Key).String("management.azure.com"),
				fields.ErrorKey(fields.ServiceMethod.Key).String("GET"),
				fields.ErrorKey(fields.ServiceErrorCode.Key).String("ServiceUnavailable"),
				fields.ErrorKey(fields.ServiceStatusCode.Key).Int(503),
			},
		},
		{
			name: "WithAuthFailedError",
			err: &auth.AuthFailedError{
				Parsed: &auth.AadErrorResponse{
					Error: "invalid_grant",
					ErrorCodes: []int{
						50076,
						50078,
						50079,
					},
					CorrelationId: "12345",
				},
			},
			wantErrReason: "service.aad.failed",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceName.Key).String("aad"),
				fields.ErrorKey(fields.ServiceErrorCode.Key).String("50076,50078,50079"),
				fields.ErrorKey(fields.ServiceStatusCode.Key).String("invalid_grant"),
				fields.ErrorKey(fields.ServiceCorrelationId.Key).String("12345"),
			},
		},
		{
			name: "WithExtServiceError",
			err: &azdext.ServiceError{
				Message:     "Rate limit exceeded",
				ErrorCode:   "RateLimitExceeded",
				StatusCode:  429,
				ServiceName: "openai.azure.com",
			},
			wantErrReason: "ext.service.openai.429",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ServiceName.Key).String("openai"),
				fields.ErrorKey(fields.ServiceHost.Key).String("openai.azure.com"),
				fields.ErrorKey(fields.ServiceStatusCode.Key).Int(429),
				fields.ErrorKey(fields.ServiceErrorCode.Key).String("RateLimitExceeded"),
			},
		},
		{
			name:           "WithContextCanceled",
			err:            context.Canceled,
			wantErrReason:  "user.canceled",
			wantErrDetails: nil,
		},
		{
			name:           "WithContextDeadlineExceeded",
			err:            context.DeadlineExceeded,
			wantErrReason:  "internal.timeout",
			wantErrDetails: nil,
		},
		{
			name:           "WithErrNoCurrentUser",
			err:            auth.ErrNoCurrentUser,
			wantErrReason:  "auth.not_logged_in",
			wantErrDetails: nil,
		},
		{
			name:           "WithWrappedErrNoCurrentUser",
			err:            fmt.Errorf("failed to create credential: %w: %w", errors.New("inner"), auth.ErrNoCurrentUser),
			wantErrReason:  "auth.not_logged_in",
			wantErrDetails: nil,
		},
		{
			name:           "WithErrToolExecutionDenied",
			err:            consent.ErrToolExecutionDenied,
			wantErrReason:  "user.tool_denied",
			wantErrDetails: nil,
		},
		{
			name:           "WithErrNotRepository",
			err:            git.ErrNotRepository,
			wantErrReason:  "internal.not_git_repo",
			wantErrDetails: nil,
		},
		{
			name:           "WithErrPreviewNotSupported",
			err:            azapi.ErrPreviewNotSupported,
			wantErrReason:  "internal.preview_not_supported",
			wantErrDetails: nil,
		},
		{
			name:           "WithErrBindMountOperationDisabled",
			err:            provisioning.ErrBindMountOperationDisabled,
			wantErrReason:  "internal.bind_mount_disabled",
			wantErrDetails: nil,
		},
		{
			name:           "WithErrRemoteHostIsNotAzDo",
			err:            fmt.Errorf("%w: https://dev.azure.com/org", pipeline.ErrRemoteHostIsNotAzDo),
			wantErrReason:  "internal.remote_not_azdo",
			wantErrDetails: nil,
		},
		{
			name: "WithDNSError",
			err: &net.DNSError{
				Err:  "no such host",
				Name: "management.azure.com",
			},
			wantErrReason: "internal.network",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("*net.DNSError"),
			},
		},
		{
			name:           "WithWrappedContextCanceled",
			err:            fmt.Errorf("operation failed: %w", context.Canceled),
			wantErrReason:  "user.canceled",
			wantErrDetails: nil,
		},
		{
			name:          "WithEOFError",
			err:           io.EOF,
			wantErrReason: "internal.network",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("*errors.errorString"),
			},
		},
		{
			name:          "WithUnexpectedEOFError",
			err:           io.ErrUnexpectedEOF,
			wantErrReason: "internal.network",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrType.String("*errors.errorString"),
			},
		},
		{
			name: "WithExtLocalError",
			err: &azdext.LocalError{
				Message:  "invalid manifest",
				Code:     "Invalid-Config",
				Category: azdext.LocalErrorCategoryValidation,
			},
			wantErrReason: "ext.validation.invalid_config",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ErrCategory.Key).String("validation"),
				fields.ErrorKey(fields.ErrCode.Key).String("invalid_config"),
			},
		},
		{
			name: "WithExtLocalErrorUnknownCategory",
			err: &azdext.LocalError{
				Message:  "some local failure",
				Code:     "Something-Bad",
				Category: azdext.LocalErrorCategory("custom"),
			},
			wantErrReason: "ext.local.something_bad",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ErrCategory.Key).String("local"),
				fields.ErrorKey(fields.ErrCode.Key).String("something_bad"),
			},
		},
		{
			name: "WithExtLocalErrorAuthDomain",
			err: &azdext.LocalError{
				Message:  "token expired",
				Code:     "token_expired",
				Category: azdext.LocalErrorCategoryAuth,
			},
			wantErrReason: "ext.auth.token_expired",
			wantErrDetails: []attribute.KeyValue{
				fields.ErrorKey(fields.ErrCategory.Key).String("auth"),
				fields.ErrorKey(fields.ErrCode.Key).String("token_expired"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			span := &mocktracing.Span{}
			MapError(tt.err, span)

			require.Equal(t, tt.wantErrReason, span.Status.Description)
			require.ElementsMatch(t, tt.wantErrDetails, span.Attributes)
		})
	}
}

func Test_cmdAsName(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{"WithNilCmd", "", ""},
		{"WithDot", ".", ""},
		{"WithFile", "TOOL", "tool"},
		{"WithFileAndExt", "tool.exe", "tool"},
		{"WithPath", "../tool.exe", "tool"},
		{"WithHiddenFile", ".TOOL", "tool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, cmdAsName(tt.cmd))
		})
	}
}

func Test_errorType(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "NilError",
			err:  nil,
			want: "<nil>",
		},
		{
			name: "SimpleError",
			err:  errors.New("simple error"),
			want: "*errors.errorString",
		},
		{
			name: "SingleUnwrapError",
			err: &exec.ExitError{
				Cmd:      "test",
				ExitCode: 1,
			},
			want: "*exec.ExitError",
		},
		{
			name: "NestedUnwrapError",
			err: func() error {
				inner := errors.New("inner error")
				return &singleUnwrapError{
					msg: "wrapped error",
					err: inner,
				}
			}(),
			want: "*errors.errorString",
		},
		{
			name: "MultipleUnwrapErrors",
			err: func() error {
				err1 := errors.New("error 1")
				err2 := errors.New("error 2")
				return &multiUnwrapError{
					errs: []error{err1, err2},
				}
			}(),
			want: "*errors.errorString,*errors.errorString",
		},
		{
			name: "MultipleUnwrapErrorsWithNil",
			err: func() error {
				err1 := errors.New("error 1")
				return &multiUnwrapError{
					errs: []error{err1, nil, errors.New("error 2")},
				}
			}(),
			want: "*errors.errorString,*errors.errorString",
		},
		{
			name: "UnwrapReturnsNil",
			err: &singleUnwrapError{
				msg: "test error",
				err: nil,
			},
			want: "*cmd.singleUnwrapError",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errorType(tt.err)
			require.Equal(t, tt.want, got)
		})
	}
}

// Test helper types for errorType tests
type singleUnwrapError struct {
	msg string
	err error
}

func (e *singleUnwrapError) Error() string {
	return e.msg
}

func (e *singleUnwrapError) Unwrap() error {
	return e.err
}

type multiUnwrapError struct {
	errs []error
}

func (e *multiUnwrapError) Error() string {
	return "multiple errors"
}

func (e *multiUnwrapError) Unwrap() []error {
	return e.errs
}

func mustMarshalJson(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func Test_isNetworkError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "NilError",
			err:  nil,
			want: false,
		},
		{
			name: "PlainError",
			err:  errors.New("something broke"),
			want: false,
		},
		{
			name: "DNSError",
			err:  &net.DNSError{Err: "no such host", Name: "example.com"},
			want: true,
		},
		{
			name: "WrappedDNSError",
			err:  fmt.Errorf("request failed: %w", &net.DNSError{Err: "no such host", Name: "example.com"}),
			want: true,
		},
		{
			name: "EOF",
			err:  io.EOF,
			want: true,
		},
		{
			name: "UnexpectedEOF",
			err:  io.ErrUnexpectedEOF,
			want: true,
		},
		{
			name: "WrappedEOF",
			err:  fmt.Errorf("reading response: %w", io.EOF),
			want: true,
		},
		{
			name: "ContextCanceled",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "NetOpError",
			err:  &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
			want: true,
		},
		{
			name: "TLSRecordHeaderError",
			err:  &tls.RecordHeaderError{Msg: "bad record"},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isNetworkError(tt.err))
		})
	}
}

// Test_PackageLevelErrorsMapped scans the azd codebase for package-level error variables
// (var Err* = errors.New/fmt.Errorf) and verifies that each is either mapped in MapError (errors.go)
// or explicitly excluded.
//
// This prevents new error variables from silently falling through to the unhelpful
// "internal.errors_errorString" default in telemetry.
//
// If this test fails, you need to either:
// 1. Add an errors.Is() check for your error variable in MapError (errors.go), OR
// 2. Add it to the excludedErrors list below with a comment explaining why.
func Test_PackageLevelErrorsMapped(t *testing.T) {
	// Package-level error variables that are intentionally NOT mapped in MapError, with reasons:
	excludedErrors := map[string]string{
		// Internal-only errors that never propagate to command-level
		"ErrDuplicateRegistration": "internal/mapper: programming error, not a runtime user error",
		"ErrInvalidRegistration":   "internal/mapper: programming error, not a runtime user error",
		"ErrNodeNotFound":          "pkg/yamlnode: internal YAML traversal error, always caught before command level",
		"ErrNodeWrongKind":         "pkg/yamlnode: internal YAML traversal error, always caught before command level",
		"ErrPropertyNotFound":      "pkg/tools/maven: internal property lookup, always caught before command level",
		"ErrResolveInstance":       "pkg/ioc: dependency injection error, caught during container resolution",
		"ErrInvalidEvent":          "pkg/ext: lifecycle event error, caught in event dispatcher",
		"ErrScriptTypeUnknown":     "pkg/ext: hook script validation, caught before command level",
		"ErrRunRequired":           "pkg/ext: hook configuration validation, caught before command level",
		"ErrUnsupportedScriptType": "pkg/ext: hook script validation, caught before command level",

		// Errors that are always caught/handled before reaching MapError
		"ErrEnsureEnvPreReqBicepCompileFailed": "caught in cmd/env.go and cmd/up.go before reaching telemetry",
		"ErrAzdOperationsNotEnabled":           "caught in pkg/project/dotnet_importer.go before reaching telemetry",
		"ErrAzCliSecretNotFound":               "caught in pkg/cmdsubst before reaching telemetry",
		"ErrNoSuchRemote":                      "caught in pkg/pipeline/pipeline_manager.go before reaching telemetry",
		"ErrRemoteHostIsNotGitHub":             "caught in pkg/pipeline and pkg/github before reaching telemetry",
		"ErrSSHNotSupported":                   "only defined, referenced via ErrRemoteHostIsNotAzDo flow",
		"ErrDeploymentNotFound":                "caught in provisioning/deployment callers before reaching telemetry",
		"ErrDeploymentsNotFound":               "caught in infra callers before reaching telemetry",
		"ErrDeploymentResourcesNotFound":       "caught in infra callers before reaching telemetry",
		"ErrNoProject":                         "caught in environment/context callers before reaching telemetry",
		"ErrContainerNotFound":                 "caught in storage blob callers before reaching telemetry",
		"ErrPlatformNotSupported":              "caught in platform config resolver before reaching telemetry",
		"ErrPlatformConfigNotFound":            "caught in platform config resolver before reaching telemetry",
		"ErrNoDefaultService":                  "caught in project manager callers before reaching telemetry",
		"ErrSourceNotFound":                    "caught in template source manager before reaching telemetry",
		"ErrSourceExists":                      "caught in template source manager before reaching telemetry",
		"ErrSourceTypeInvalid":                 "caught in template source manager before reaching telemetry",
		"ErrRepositoryNameInUse":               "caught in pipeline config flow before reaching telemetry",
		"ErrResourceNotFound":                  "caught in kubectl callers before reaching telemetry",
		"ErrResourceNotReady":                  "caught in kubectl callers before reaching telemetry",

		// Duplicate definitions (same error variable defined in multiple packages)
		"ErrDebuggerAborted": "defined in both cmd/middleware and pkg/azdext, handled at debug middleware level",

		// Agent consent errors that map to user-initiated cancellation
		"ErrSamplingDenied":    "agent consent: similar to user.canceled, low frequency",
		"ErrElicitationDenied": "agent consent: similar to user.canceled, low frequency",

		// UX cancellation that is always joined with context.Canceled (already mapped as user.canceled)
		"ErrCancelled": "pkg/ux: always errors.Join'd with ctx.Err(), caught by context.Canceled check",

		// Environment management errors surfaced as user-facing messages with suggestions
		"ErrExists":                     "environment: user-facing with suggestion, wrapped before reaching telemetry",
		"ErrNotFound":                   "environment: user-facing with suggestion, wrapped before reaching telemetry",
		"ErrNameNotSpecified":           "environment: user-facing with suggestion, wrapped before reaching telemetry",
		"ErrDefaultEnvironmentNotFound": "environment: user-facing with suggestion, wrapped before reaching telemetry",

		// Storage/auth errors caught in data store callers
		"ErrAccessDenied":     "storage blob: caught in environment data store callers before reaching telemetry",
		"ErrInvalidContainer": "storage blob: caught in environment data store callers before reaching telemetry",

		// AI model/quota errors caught in extension callers
		"ErrQuotaLocationRequired": "pkg/ai: caught in AI extension callers before reaching telemetry",
		"ErrModelNotFound":         "pkg/ai: caught in AI extension callers before reaching telemetry",
		"ErrNoDeploymentMatch":     "pkg/ai: caught in AI extension callers before reaching telemetry",

		// Auth errors that could propagate but are rare edge cases
		"ErrAzCliNotLoggedIn":         "pkg/azapi: az CLI auth delegation, wrapped by auth.Manager",
		"ErrAzCliRefreshTokenExpired": "pkg/azapi: az CLI auth delegation, wrapped by auth.Manager",

		// Pipeline/CI errors handled in pipeline config flow
		"ErrAuthNotSupported": "pkg/pipeline: caught in pipeline config flow before reaching telemetry",

		// Resource selection errors surfaced as user-facing prompts
		"ErrNoResourcesFound":   "pkg/prompt: interactive prompt error, caught in command callers",
		"ErrNoResourceSelected": "pkg/prompt: interactive prompt error, caught in command callers",

		// Template errors caught in init flow
		"ErrTemplateNotFound": "pkg/templates: caught in init/template callers before reaching telemetry",

		// GitHub CLI errors caught in pipeline config
		"ErrGitHubCliNotLoggedIn": "pkg/tools/github: caught in pipeline config flow before reaching telemetry",
		"ErrUserNotAuthorized":    "pkg/tools/github: caught in pipeline config flow before reaching telemetry",

		// Extension management errors caught in extension callers
		"ErrExtensionNotFound":          "pkg/extensions: caught in extension manager callers",
		"ErrInstalledExtensionNotFound": "pkg/extensions: caught in extension manager callers",
		"ErrRegistryExtensionNotFound":  "pkg/extensions: caught in extension manager callers",
	}

	// Find the azd root directory (two levels up from internal/cmd)
	azdRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	// Read errors.go to get the list of error variable references
	errorsGoPath := filepath.Join(azdRoot, "internal", "cmd", "errors.go")
	errorsGoContent, err := os.ReadFile(errorsGoPath)
	require.NoError(t, err)
	errorsGoStr := string(errorsGoContent)

	var unmapped []string

	// Walk the source tree and parse each Go file using go/ast to find
	// package-level var declarations (including var blocks) of the form:
	//   var ErrX = errors.New(...)
	//   var ErrX = fmt.Errorf(...)
	err = filepath.Walk(azdRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			base := filepath.Base(path)
			if base == "vendor" || base == "extensions" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return nil // skip unparseable files
		}

		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.VAR {
				continue
			}

			for _, spec := range genDecl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}

				for i, name := range valueSpec.Names {
					if !strings.HasPrefix(name.Name, "Err") {
						continue
					}

					if i >= len(valueSpec.Values) {
						continue
					}

					// Check if the value is errors.New(...) or fmt.Errorf(...)
					callExpr, ok := valueSpec.Values[i].(*ast.CallExpr)
					if !ok {
						continue
					}

					if !isErrorConstructorCall(callExpr) {
						continue
					}

					errVarName := name.Name

					if _, ok := excludedErrors[errVarName]; ok {
						continue
					}

					if !strings.Contains(errorsGoStr, errVarName) {
						relPath, _ := filepath.Rel(azdRoot, path)
						unmapped = append(unmapped, fmt.Sprintf("  %s (defined in %s)", errVarName, relPath))
					}
				}
			}
		}

		return nil
	})
	require.NoError(t, err)

	if len(unmapped) > 0 {
		t.Errorf(
			"Found %d package-level error variable(s) not mapped in MapError (internal/cmd/errors.go).\n"+
				"Each error variable should have an errors.Is() check in MapError for meaningful telemetry,\n"+
				"or be added to excludedErrors in this test with a reason.\n\n"+
				"Unmapped errors:\n%s",
			len(unmapped),
			strings.Join(unmapped, "\n"),
		)
	}
}

// isErrorConstructorCall checks if a call expression is errors.New(...) or fmt.Errorf(...).
func isErrorConstructorCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}

	return (ident.Name == "errors" && sel.Sel.Name == "New") ||
		(ident.Name == "fmt" && sel.Sel.Name == "Errorf")
}
