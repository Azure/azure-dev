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

	if updateErr, ok := errors.AsType[*update.UpdateError](err); ok {
		errCode = updateErr.Code
	} else if _, ok := errors.AsType[*auth.ReLoginRequiredError](err); ok {
		errCode = "auth.login_required"
	} else if errWithSuggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err); ok {
		errCode = "error.suggestion"
		span.SetAttributes(fields.ErrType.String(classifySuggestionType(errWithSuggestion.Unwrap())))
	} else if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
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
	} else if armDeployErr, ok := errors.AsType[*azapi.AzureDeploymentError](err); ok {
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
	} else if extServiceErr, ok := errors.AsType[*azdext.ServiceError](err); ok {
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
	} else if extLocalErr, ok := errors.AsType[*azdext.LocalError](err); ok {
		domain := string(azdext.NormalizeLocalErrorCategory(extLocalErr.Category))
		code := normalizeCodeSegment(extLocalErr.Code, "failed")

		errDetails = append(errDetails,
			fields.ErrCategory.String(domain),
			fields.ErrCode.String(code),
		)

		errCode = fmt.Sprintf("ext.%s.%s", domain, code)
	} else if _, ok := errors.AsType[*extensions.ExtensionRunError](err); ok {
		errCode = "ext.run.failed"
	} else if toolExecErr, ok := errors.AsType[*exec.ExitError](err); ok {
		toolName := "other"
		cmdName := cmdAsName(toolExecErr.Cmd)
		if cmdName != "" {
			toolName = cmdName
		}

		errDetails = append(errDetails,
			fields.ToolExitCode.Int(toolExecErr.ExitCode),
			fields.ToolName.String(toolName))

		errCode = fmt.Sprintf("tool.%s.failed", toolName)
	} else if toolCheckErr, ok := errors.AsType[*tools.MissingToolErrors](err); ok {
		if len(toolCheckErr.ToolNames) == 1 {
			toolName := toolCheckErr.ToolNames[0]
			errCode = fmt.Sprintf("tool.%s.missing", toolName)
			errDetails = append(errDetails, fields.ToolName.String(toolName))
		} else {
			errCode = "tool.multiple.missing"
			errDetails = append(errDetails, fields.ToolName.String(strings.Join(toolCheckErr.ToolNames, ",")))
		}
	} else if authFailedErr, ok := errors.AsType[*auth.AuthFailedError](err); ok {
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
	} else if errors.Is(err, terminal.InterruptErr) {
		errCode = "user.canceled"
	} else if errors.Is(err, context.Canceled) {
		errCode = "user.canceled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		errCode = "internal.timeout"
	} else if errors.Is(err, auth.ErrNoCurrentUser) {
		errCode = "auth.not_logged_in"
	} else if errors.Is(err, consent.ErrToolExecutionDenied) {
		errCode = "user.tool_denied"
	} else if errors.Is(err, git.ErrNotRepository) {
		errCode = "internal.not_git_repo"
	} else if errors.Is(err, azapi.ErrPreviewNotSupported) {
		errCode = "internal.preview_not_supported"
	} else if errors.Is(err, provisioning.ErrBindMountOperationDisabled) {
		errCode = "internal.bind_mount_disabled"
	} else if errors.Is(err, update.ErrNeedsElevation) {
		errCode = "update.elevationRequired"
	} else if errors.Is(err, pipeline.ErrRemoteHostIsNotAzDo) {
		errCode = "internal.remote_not_azdo"
	} else if errors.Is(err, internal.ErrExtensionNotFound) {
		errCode = "internal.extension_not_found"
	} else if errors.Is(err, internal.ErrExtensionTokenFailed) {
		errCode = "internal.extension_error"
	} else if isNetworkError(err) {
		errCode = "internal.network"
		errType := errorType(err)
		span.SetAttributes(fields.ErrType.String(errType))
	} else {
		errType := errorType(err)
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

// classifySentinel checks if the error matches a known sentinel
// and returns the corresponding telemetry code, or "" if no match.
func classifySentinel(err error) string {
	switch {
	case errors.Is(err, internal.ErrInfraNotProvisioned):
		return "internal.infra_not_provisioned"
	case errors.Is(err, internal.ErrFromPackageWithAll),
		errors.Is(err, internal.ErrFromPackageNoService):
		return "internal.invalid_flag_combination"
	case errors.Is(err, internal.ErrCannotChangeSubscription):
		return "internal.cannot_change_subscription"
	case errors.Is(err, internal.ErrCannotChangeLocation):
		return "internal.cannot_change_location"
	case errors.Is(err, internal.ErrPreviewMultipleLayers):
		return "internal.preview_multiple_layers"
	case errors.Is(err, internal.ErrNoKeyNameProvided),
		errors.Is(err, internal.ErrNoEnvValuesProvided),
		errors.Is(err, internal.ErrInvalidFlagCombination):
		return "internal.invalid_args"
	case errors.Is(err, internal.ErrKeyNotFound):
		return "internal.key_not_found"
	case errors.Is(err, internal.ErrNoEnvironmentsFound):
		return "internal.no_environments_found"
	case errors.Is(err, internal.ErrLoginDisabledDelegatedMode):
		return "auth.login_disabled_delegated"
	case errors.Is(err, internal.ErrBranchRequiresTemplate),
		errors.Is(err, internal.ErrMultipleInitModes):
		return "internal.invalid_args"
	case errors.Is(err, environment.ErrNotFound):
		return "internal.env_not_found"
	case errors.Is(err, azdcontext.ErrNoProject):
		return "internal.no_project"
	case errors.Is(err, internal.ErrNoArgsProvided),
		errors.Is(err, internal.ErrInvalidArgValue):
		return "internal.invalid_args"
	case errors.Is(err, internal.ErrConfigKeyNotFound):
		return "internal.config_key_not_found"
	case errors.Is(err, internal.ErrExtensionNotFound):
		return "internal.extension_not_found"
	case errors.Is(err, internal.ErrServiceNotFound):
		return "internal.service_not_found"
	case errors.Is(err, internal.ErrNoExtensionsAvailable):
		return "internal.no_extensions_available"
	case errors.Is(err, internal.ErrValidationFailed):
		return "internal.validation_failed"
	case errors.Is(err, internal.ErrUnsupportedOperation):
		return "internal.unsupported_operation"
	case errors.Is(err, internal.ErrExtensionTokenFailed):
		return "internal.extension_error"
	case errors.Is(err, internal.ErrMcpToolsLoadFailed):
		return "internal.mcp_error"
	case errors.Is(err, internal.ErrResourceNotConfigured):
		return "internal.resource_not_found"
	case errors.Is(err, internal.ErrOperationCancelled):
		return "internal.operation_cancelled"
	default:
		return ""
	}
}

// classifySuggestionType returns a telemetry error type string for an inner error wrapped by ErrorWithSuggestion.
// It preserves the suggestion result code while improving the error.type attribute when the inner error is structured.
func classifySuggestionType(err error) string {
	if code := classifySentinel(err); code != "" {
		return code
	}

	if updateErr, ok := errors.AsType[*update.UpdateError](err); ok {
		return updateErr.Code
	}

	if _, ok := errors.AsType[*auth.ReLoginRequiredError](err); ok {
		return "auth.login_required"
	}

	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		serviceName := "other"
		statusCode := -1

		if respErr.RawResponse != nil {
			statusCode = respErr.RawResponse.StatusCode
			if respErr.RawResponse.Request != nil {
				serviceName, _ = mapService(respErr.RawResponse.Request.Host)
			}
		}

		return fmt.Sprintf("service.%s.%d", serviceName, statusCode)
	}

	if armDeployErr, ok := errors.AsType[*azapi.AzureDeploymentError](err); ok {
		operationName := armDeployErr.Operation
		if operationName == azapi.DeploymentOperationDeploy {
			operationName = "deployment"
		}

		return fmt.Sprintf("service.arm.%s.failed", operationName)
	}

	if extServiceErr, ok := errors.AsType[*azdext.ServiceError](err); ok {
		serviceName := ""
		if extServiceErr.ServiceName != "" {
			serviceName, _ = mapService(extServiceErr.ServiceName)
		}

		switch {
		case extServiceErr.ErrorCode != "":
			return fmt.Sprintf("ext.service.%s", normalizeCodeSegment(extServiceErr.ErrorCode, "failed"))
		case extServiceErr.StatusCode > 0 && serviceName != "":
			return fmt.Sprintf("ext.service.%s.%d", serviceName, extServiceErr.StatusCode)
		case extServiceErr.StatusCode > 0:
			return fmt.Sprintf("ext.service.unknown.%d", extServiceErr.StatusCode)
		default:
			return "ext.service.unknown.failed"
		}
	}

	if extLocalErr, ok := errors.AsType[*azdext.LocalError](err); ok {
		domain := string(azdext.NormalizeLocalErrorCategory(extLocalErr.Category))
		code := normalizeCodeSegment(extLocalErr.Code, "failed")

		return fmt.Sprintf("ext.%s.%s", domain, code)
	}

	if _, ok := errors.AsType[*extensions.ExtensionRunError](err); ok {
		return "ext.run.failed"
	}

	if toolExecErr, ok := errors.AsType[*exec.ExitError](err); ok {
		toolName := "other"
		if cmdName := cmdAsName(toolExecErr.Cmd); cmdName != "" {
			toolName = cmdName
		}

		return fmt.Sprintf("tool.%s.failed", toolName)
	}

	if toolCheckErr, ok := errors.AsType[*tools.MissingToolErrors](err); ok {
		if len(toolCheckErr.ToolNames) == 1 {
			return fmt.Sprintf("tool.%s.missing", toolCheckErr.ToolNames[0])
		}

		return "tool.multiple.missing"
	}

	if _, ok := errors.AsType[*auth.AuthFailedError](err); ok {
		return "service.aad.failed"
	}

	if errors.Is(err, terminal.InterruptErr) || errors.Is(err, context.Canceled) {
		return "user.canceled"
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return "internal.timeout"
	}

	if errors.Is(err, auth.ErrNoCurrentUser) {
		return "auth.not_logged_in"
	}

	if errors.Is(err, consent.ErrToolExecutionDenied) {
		return "user.tool_denied"
	}

	if errors.Is(err, git.ErrNotRepository) {
		return "internal.not_git_repo"
	}

	if errors.Is(err, azapi.ErrPreviewNotSupported) {
		return "internal.preview_not_supported"
	}

	if errors.Is(err, provisioning.ErrBindMountOperationDisabled) {
		return "internal.bind_mount_disabled"
	}

	if errors.Is(err, update.ErrNeedsElevation) {
		return "update.elevationRequired"
	}

	if errors.Is(err, pipeline.ErrRemoteHostIsNotAzDo) {
		return "internal.remote_not_azdo"
	}

	if errors.Is(err, internal.ErrExtensionNotFound) {
		return "internal.extension_not_found"
	}

	if errors.Is(err, internal.ErrExtensionTokenFailed) {
		return "internal.extension_error"
	}

	if isNetworkError(err) {
		return "internal.network"
	}

	return errorType(err)
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
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return true
	}

	// Check for network operation errors (connection refused, timeout, etc.)
	if _, ok := errors.AsType[*net.OpError](err); ok {
		return true
	}

	// Check for TLS errors
	if _, ok := errors.AsType[*tls.RecordHeaderError](err); ok {
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
