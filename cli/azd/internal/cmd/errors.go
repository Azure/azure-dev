// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
	"github.com/azure/azure-dev/cli/azd/pkg/update"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// MapError maps the given error to a telemetry span, setting relevant status and attributes.
func MapError(err error, span tracing.Span) {
	var errCode string
	var errDetails []attribute.KeyValue

	// external service errors
	var respErr *azcore.ResponseError
	var armDeployErr *azapi.AzureDeploymentError
	var authFailedErr *auth.AuthFailedError
	var extServiceErr *azdext.ServiceError
	var extLocalErr *azdext.LocalError

	// external tool errors
	var toolExecErr *exec.ExitError
	var toolCheckErr *tools.MissingToolErrors
	var extensionRunErr *extensions.ExtensionRunError

	// internal errors
	var errWithSuggestion *internal.ErrorWithSuggestion
	var loginErr *auth.ReLoginRequiredError
	var updateErr *update.UpdateError

	// If the error is wrapped in ErrorWithSuggestion, unwrap it first so the inner error
	// can be classified by its actual type (ResponseError, AzureDeploymentError, etc.)
	// instead of being bucketed as the opaque "error.suggestion".
	classifyErr := err
	if errors.As(err, &errWithSuggestion) {
		if inner := errWithSuggestion.Unwrap(); inner != nil {
			classifyErr = inner
		}
	}

	if errors.As(classifyErr, &updateErr) {
		errCode = updateErr.Code
	} else if errors.As(classifyErr, &loginErr) {
		errCode = "auth.login_required"
	} else if errors.As(classifyErr, &respErr) {
		serviceName := "other"
		statusCode := -1
		errDetails = append(errDetails, fields.ServiceErrorCode.String(respErr.ErrorCode))

		if respErr.RawResponse != nil {
			statusCode = respErr.RawResponse.StatusCode
			errDetails = append(errDetails, fields.ServiceStatusCode.Int(statusCode))

			if respErr.RawResponse.Request != nil {
				var hostName string
				serviceName, hostName = mapService(respErr.RawResponse.Request.Host)
				errDetails = append(errDetails,
					fields.ServiceHost.String(hostName),
					fields.ServiceMethod.String(respErr.RawResponse.Request.Method),
					fields.ServiceName.String(serviceName),
				)
			}
		}

		errCode = fmt.Sprintf("service.%s.%d", serviceName, statusCode)
	} else if errors.As(classifyErr, &armDeployErr) {
		errDetails = append(errDetails, fields.ServiceName.String("arm"))
		codes := []*deploymentErrorCode{}
		var collect func(details []*azapi.DeploymentErrorLine, frame int)
		collect = func(details []*azapi.DeploymentErrorLine, frame int) {
			code := collectCode(details, frame)
			if code != nil {
				codes = append(codes, code)
				frame = frame + 1
			}

			for _, detail := range details {
				if detail.Inner != nil {
					collect(detail.Inner, frame)
				}
			}
		}

		collect([]*azapi.DeploymentErrorLine{armDeployErr.Details}, 0)
		if len(codes) > 0 {
			if codesJson, err := json.Marshal(codes); err != nil {
				log.Println("telemetry: failed to marshal arm error codes", err)
			} else {
				errDetails = append(errDetails, fields.ServiceErrorCode.String(string(codesJson)))
			}
		}

		// Use operation-specific error code if available
		operation := armDeployErr.Operation
		if operation == azapi.DeploymentOperationDeploy {
			// use 'deployment' instead of 'deploy' for consistency with prior naming
			operation = "deployment"
		}
		errCode = fmt.Sprintf("service.arm.%s.failed", operation)
	} else if errors.As(classifyErr, &extServiceErr) {
		// Handle structured service errors from extensions.
		// Emit whatever details are available rather than requiring all fields.
		serviceName := ""
		if extServiceErr.ServiceName != "" {
			var hostDomain string
			serviceName, hostDomain = mapService(extServiceErr.ServiceName)
			errDetails = append(errDetails,
				fields.ServiceName.String(serviceName),
				fields.ServiceHost.String(hostDomain),
			)
		}
		if extServiceErr.StatusCode > 0 {
			errDetails = append(errDetails, fields.ServiceStatusCode.Int(extServiceErr.StatusCode))
		}
		if extServiceErr.ErrorCode != "" {
			errDetails = append(errDetails, fields.ServiceErrorCode.String(extServiceErr.ErrorCode))
		}

		// Use operation.errorCode (e.g. "ext.service.start_container.invalid_payload") for actionable
		// classification instead of host.statusCode which groups unrelated failures together.
		switch {
		case extServiceErr.ErrorCode != "":
			errCode = fmt.Sprintf("ext.service.%s", normalizeCodeSegment(extServiceErr.ErrorCode, "failed"))
		case extServiceErr.StatusCode > 0 && serviceName != "":
			errCode = fmt.Sprintf("ext.service.%s.%d", serviceName, extServiceErr.StatusCode)
		case extServiceErr.StatusCode > 0:
			errCode = fmt.Sprintf("ext.service.unknown.%d", extServiceErr.StatusCode)
		default:
			errCode = "ext.service.unknown.failed"
		}
	} else if errors.As(classifyErr, &extLocalErr) {
		domain := string(azdext.NormalizeLocalErrorCategory(extLocalErr.Category))
		code := normalizeCodeSegment(extLocalErr.Code, "failed")

		errDetails = append(errDetails,
			fields.ErrCategory.String(domain),
			fields.ErrCode.String(code),
		)

		errCode = fmt.Sprintf("ext.%s.%s", domain, code)
	} else if errors.As(classifyErr, &extensionRunErr) {
		errCode = "ext.run.failed"
	} else if errors.As(classifyErr, &toolExecErr) {
		toolName := "other"
		cmdName := cmdAsName(toolExecErr.Cmd)
		if cmdName != "" {
			toolName = cmdName
		}

		errDetails = append(errDetails,
			fields.ToolExitCode.Int(toolExecErr.ExitCode),
			fields.ToolName.String(toolName))

		errCode = fmt.Sprintf("tool.%s.failed", toolName)
	} else if errors.As(classifyErr, &toolCheckErr) {
		if len(toolCheckErr.ToolNames) == 1 {
			toolName := toolCheckErr.ToolNames[0]
			errCode = fmt.Sprintf("tool.%s.missing", toolName)
			errDetails = append(errDetails, fields.ToolName.String(toolName))
		} else {
			errCode = "tool.multiple.missing"
			errDetails = append(errDetails, fields.ToolName.String(strings.Join(toolCheckErr.ToolNames, ",")))
		}
	} else if errors.As(classifyErr, &authFailedErr) {
		errDetails = append(errDetails, fields.ServiceName.String("aad"))
		if authFailedErr.Parsed != nil {
			codes := make([]string, 0, len(authFailedErr.Parsed.ErrorCodes))
			for _, code := range authFailedErr.Parsed.ErrorCodes {
				codes = append(codes, fmt.Sprintf("%d", code))
			}
			serviceErr := strings.Join(codes, ",")
			errDetails = append(errDetails,
				fields.ServiceStatusCode.String(authFailedErr.Parsed.Error),
				fields.ServiceErrorCode.String(serviceErr),
				fields.ServiceCorrelationId.String(authFailedErr.Parsed.CorrelationId))
		}
		errCode = "service.aad.failed"
	} else if errors.Is(classifyErr, terminal.InterruptErr) {
		errCode = "user.canceled"
	} else if errors.Is(classifyErr, context.Canceled) {
		errCode = "user.canceled"
	} else if errors.Is(classifyErr, context.DeadlineExceeded) {
		errCode = "internal.timeout"
	} else if errors.Is(classifyErr, auth.ErrNoCurrentUser) {
		errCode = "auth.not_logged_in"
	} else if errors.Is(classifyErr, consent.ErrToolExecutionDenied) {
		errCode = "user.tool_denied"
	} else if errors.Is(classifyErr, git.ErrNotRepository) {
		errCode = "internal.not_git_repo"
	} else if errors.Is(classifyErr, azapi.ErrPreviewNotSupported) {
		errCode = "internal.preview_not_supported"
	} else if errors.Is(classifyErr, provisioning.ErrBindMountOperationDisabled) {
		errCode = "internal.bind_mount_disabled"
	} else if errors.Is(classifyErr, update.ErrNeedsElevation) {
		errCode = "update.elevationRequired"
	} else if errors.Is(classifyErr, pipeline.ErrRemoteHostIsNotAzDo) {
		errCode = "internal.remote_not_azdo"
	} else if errors.Is(classifyErr, internal.ErrInfraNotProvisioned) {
		errCode = "internal.infra_not_provisioned"
	} else if errors.Is(classifyErr, internal.ErrFromPackageWithAll) ||
		errors.Is(classifyErr, internal.ErrFromPackageNoService) {
		errCode = "internal.invalid_flag_combination"
	} else if errors.Is(classifyErr, internal.ErrCannotChangeSubscription) {
		errCode = "internal.cannot_change_subscription"
	} else if errors.Is(classifyErr, internal.ErrCannotChangeLocation) {
		errCode = "internal.cannot_change_location"
	} else if errors.Is(classifyErr, internal.ErrPreviewMultipleLayers) {
		errCode = "internal.preview_multiple_layers"
	} else if errors.Is(classifyErr, internal.ErrNoKeyNameProvided) ||
		errors.Is(classifyErr, internal.ErrNoEnvValuesProvided) ||
		errors.Is(classifyErr, internal.ErrInvalidFlagCombination) {
		errCode = "internal.invalid_args"
	} else if errors.Is(classifyErr, internal.ErrKeyNotFound) {
		errCode = "internal.key_not_found"
	} else if errors.Is(classifyErr, internal.ErrNoEnvironmentsFound) {
		errCode = "internal.no_environments_found"
	} else if errors.Is(classifyErr, internal.ErrLoginDisabledDelegatedMode) {
		errCode = "auth.login_disabled_delegated"
	} else if errors.Is(classifyErr, internal.ErrBranchRequiresTemplate) ||
		errors.Is(classifyErr, internal.ErrMultipleInitModes) {
		errCode = "internal.invalid_args"
	} else if errors.Is(classifyErr, environment.ErrNotFound) {
		errCode = "internal.env_not_found"
	} else if errors.Is(classifyErr, azdcontext.ErrNoProject) {
		errCode = "internal.no_project"
	} else if errors.Is(classifyErr, internal.ErrNoArgsProvided) ||
		errors.Is(classifyErr, internal.ErrInvalidArgValue) {
		errCode = "internal.invalid_args"
	} else if errors.Is(classifyErr, internal.ErrConfigKeyNotFound) {
		errCode = "internal.config_key_not_found"
	} else if errors.Is(classifyErr, internal.ErrExtensionNotFound) {
		errCode = "internal.extension_not_found"
	} else if errors.Is(classifyErr, internal.ErrServiceNotFound) {
		errCode = "internal.service_not_found"
	} else if errors.Is(classifyErr, internal.ErrNoExtensionsAvailable) {
		errCode = "internal.no_extensions_available"
	} else if errors.Is(classifyErr, internal.ErrValidationFailed) {
		errCode = "internal.validation_failed"
	} else if errors.Is(classifyErr, internal.ErrUnsupportedOperation) {
		errCode = "internal.unsupported_operation"
	} else if errors.Is(classifyErr, internal.ErrExtensionTokenFailed) {
		errCode = "internal.extension_error"
	} else if errors.Is(classifyErr, internal.ErrMcpToolsLoadFailed) {
		errCode = "internal.mcp_error"
	} else if errors.Is(classifyErr, internal.ErrResourceNotConfigured) {
		errCode = "internal.resource_not_found"
	} else if errors.Is(classifyErr, internal.ErrOperationCancelled) {
		errCode = "internal.operation_cancelled"
	} else if isNetworkError(classifyErr) {
		errCode = "internal.network"
		errType := errorType(classifyErr)
		span.SetAttributes(fields.ErrType.String(errType))
	} else {
		errType := errorType(classifyErr)
		span.SetAttributes(fields.ErrType.String(errType))
		errCode = fmt.Sprintf("internal.%s",
			strings.ReplaceAll(strings.ReplaceAll(errType, ".", "_"), "*", ""))
	}

	if len(errDetails) > 0 {
		for i, detail := range errDetails {
			errDetails[i].Key = fields.ErrorKey(detail.Key)
		}

		span.SetAttributes(errDetails...)
	}

	span.SetStatus(codes.Error, errCode)
}

// errorType returns the type name of the given error, unwrapping as needed to find the root cause(s).
func errorType(err error) string {
	if err == nil {
		return "<nil>"
	}

	//nolint:errorlint // Type switch is intentionally used to check for Unwrap() methods
	for {
		switch x := err.(type) {
		case interface{ Unwrap() error }:
			err = x.Unwrap()
			if err == nil {
				return reflect.TypeOf(x).String()
			}
		case interface{ Unwrap() []error }:
			result := ""
			for _, err := range x.Unwrap() {
				if err == nil {
					continue
				}
				if result != "" {
					result += ","
				}

				result += reflect.TypeOf(err).String()
			}
			return result
		default:
			return reflect.TypeOf(x).String()
		}
	}
}

type deploymentErrorCode struct {
	Code  string `json:"error.code"`
	Frame int    `json:"error.frame"`
}

func collectCode(lines []*azapi.DeploymentErrorLine, frame int) *deploymentErrorCode {
	if len(lines) == 0 {
		return nil
	}

	sb := strings.Builder{}
	for _, line := range lines {
		if line != nil && line.Code != "" {
			if sb.Len() > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(line.Code)
		}
	}

	if sb.Len() == 0 {
		return nil
	}

	return &deploymentErrorCode{
		Frame: frame,
		Code:  sb.String(),
	}
}

// mapService maps the given hostname to a service and host domain for telemetry purposes.
//
// The host name is validated against well-known domains, and if a match is found, the service
// and corresponding anonymized domain is returned. If the domain name is unrecognized,
// it is returned as "other", "other".
func mapService(host string) (service string, hostDomain string) {
	for _, domain := range fields.Domains {
		if strings.HasSuffix(host, domain.Name) {
			return domain.Service, domain.Name
		}
	}

	return "other", "other"
}

// isNetworkError returns true if the error is a network-related error such as
// DNS resolution failure, connection refused, TLS handshake failure, or connection reset.
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}

	// Check for DNS errors
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}

	// Check for network operation errors (connection refused, timeout, etc.)
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}

	// Check for TLS errors
	var tlsRecordErr *tls.RecordHeaderError
	if errors.As(err, &tlsRecordErr) {
		return true
	}

	// Check for EOF (connection closed unexpectedly)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	return false
}

func cmdAsName(cmd string) string {
	cmd = filepath.Base(cmd)
	if len(cmd) > 0 && cmd[0] == '.' { // hidden file, simply ignore the first period
		if len(cmd) == 1 {
			return ""
		}

		cmd = cmd[1:]
	}

	for i := range cmd {
		if cmd[i] == '.' { // do not include any extensions
			cmd = cmd[:i]
			break
		}
	}

	return strings.ToLower(cmd)
}

var (
	codeSegmentRegex    = regexp.MustCompile(`[^a-z0-9_]+`)
	codeSegmentReplacer = strings.NewReplacer("-", "_")
)

// normalizeCodeSegment normalizes a dot-separated error code for telemetry.
// Each segment between dots is lowercased, sanitized to [a-z0-9_], and preserved.
// Empty input returns the fallback value.
func normalizeCodeSegment(value string, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}

	parts := strings.Split(value, ".")
	for i, part := range parts {
		part = codeSegmentReplacer.Replace(part)
		part = codeSegmentRegex.ReplaceAllString(part, "_")
		parts[i] = strings.Trim(part, "_")
	}

	result := strings.Join(parts, ".")
	result = strings.Trim(result, ".")
	if result == "" {
		return fallback
	}

	return result
}
