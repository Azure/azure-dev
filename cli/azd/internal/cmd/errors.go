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
	"strings"

	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/pipeline"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/git"
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

	// external tool errors
	var toolExecErr *exec.ExitError
	var toolCheckErr *tools.MissingToolErrors
	var extensionRunErr *extensions.ExtensionRunError

	// internal errors
	var errWithSuggestion *internal.ErrorWithSuggestion
	var loginErr *auth.ReLoginRequiredError

	if errors.As(err, &loginErr) {
		errCode = "auth.login_required"
	} else if errors.As(err, &errWithSuggestion) {
		errCode = "error.suggestion"
		errType := errorType(errWithSuggestion.Unwrap())
		span.SetAttributes(fields.ErrType.String(errType))
	} else if errors.As(err, &respErr) {
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
	} else if errors.As(err, &armDeployErr) {
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
	} else if errors.As(err, &extensionRunErr) {
		errCode = "ext.run.failed"
	} else if errors.As(err, &extServiceErr) {
		// Handle structured service errors from extensions
		if extServiceErr.StatusCode > 0 && extServiceErr.ServiceName != "" {
			serviceName, hostDomain := mapService(extServiceErr.ServiceName)
			errDetails = append(errDetails,
				fields.ServiceName.String(serviceName),
				fields.ServiceHost.String(hostDomain),
				fields.ServiceStatusCode.Int(extServiceErr.StatusCode),
			)
			if extServiceErr.ErrorCode != "" {
				errDetails = append(errDetails, fields.ServiceErrorCode.String(extServiceErr.ErrorCode))
			}
			errCode = fmt.Sprintf("ext.service.%s.%d", serviceName, extServiceErr.StatusCode)
		} else {
			errCode = "ext.service.failed"
		}
	} else if errors.As(err, &toolExecErr) {
		toolName := "other"
		cmdName := cmdAsName(toolExecErr.Cmd)
		if cmdName != "" {
			toolName = cmdName
		}

		errDetails = append(errDetails,
			fields.ToolExitCode.Int(toolExecErr.ExitCode),
			fields.ToolName.String(toolName))

		errCode = fmt.Sprintf("tool.%s.failed", toolName)
	} else if errors.As(err, &toolCheckErr) {
		if len(toolCheckErr.ToolNames) == 1 {
			toolName := toolCheckErr.ToolNames[0]
			errCode = fmt.Sprintf("tool.%s.missing", toolName)
			errDetails = append(errDetails, fields.ToolName.String(toolName))
		} else {
			errCode = "tool.multiple.missing"
			errDetails = append(errDetails, fields.ToolName.String(strings.Join(toolCheckErr.ToolNames, ",")))
		}
	} else if errors.As(err, &authFailedErr) {
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
	} else if errors.Is(err, pipeline.ErrRemoteHostIsNotAzDo) {
		errCode = "internal.remote_not_azdo"
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
