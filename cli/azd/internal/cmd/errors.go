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
	"regexp"
	"strings"

	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/errchain"
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

// MapError maps the given error to a telemetry span, setting status (the
// AppInsights ResultCode), a small set of error.* attributes, and the
// full wrapped-type chain. The classification ladder lives in classify;
// this function is responsible only for stamping its result onto the
// span.
func MapError(err error, span tracing.Span) {
	code, attrs := classify(err)

	// Always emit the wrapped-error type chain so engineers can see
	// what hides behind a generic ResultCode like internal.unclassified
	// without needing to repro. Type names are code-defined and PII-free.
	span.SetAttributes(fields.ErrChainTypes.StringSlice(errchain.Types(err)))

	if len(attrs) > 0 {
		// Prefix all detail attributes with "error." so they group in
		// AppInsights without colliding with span-scope keys.
		for i, a := range attrs {
			attrs[i].Key = fields.ErrorKey(a.Key)
		}
		span.SetAttributes(attrs...)
	}

	span.SetStatus(codes.Error, code)
}

// classify runs the typed/sentinel decision tree and returns the
// telemetry ResultCode together with any structured attributes the
// matched branch wants to expose. Attribute keys returned from this
// function are NOT yet "error."-prefixed — MapError applies the prefix.
//
// Ordering matters: wrapper types that intentionally control outer
// classification, such as ErrorWithSuggestion, must run before their
// wrapped typed errors. The generic fallback must remain last.
//
// The branch count mirrors azd's typed error surface; splitting it
// makes the telemetry contract harder to audit.
//
//nolint:gocyclo
func classify(err error) (string, []attribute.KeyValue) {
	if err == nil {
		// Preserve historical behavior for the nil-error edge case.
		return "internal.<nil>", []attribute.KeyValue{fields.ErrType.String("<nil>")}
	}

	if updateErr, ok := errors.AsType[*update.UpdateError](err); ok {
		return updateErr.Code, nil
	}
	if _, ok := errors.AsType[*auth.ReLoginRequiredError](err); ok {
		return "auth.login_required", []attribute.KeyValue{fields.ErrCategory.String("auth")}
	}
	if errWithSuggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err); ok {
		return classifyErrorWithSuggestion(errWithSuggestion)
	}
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		return classifyResponseError(respErr)
	}
	if armDeployErr, ok := errors.AsType[*azapi.AzureDeploymentError](err); ok {
		return classifyArmDeployError(armDeployErr)
	}
	if extServiceErr, ok := errors.AsType[*azdext.ServiceError](err); ok {
		return classifyExtServiceError(extServiceErr)
	}
	if extLocalErr, ok := errors.AsType[*azdext.LocalError](err); ok {
		return classifyExtLocalError(extLocalErr)
	}
	if _, ok := errors.AsType[*extensions.ExtensionRunError](err); ok {
		return "ext.run.failed", nil
	}
	if toolExecErr, ok := errors.AsType[*exec.ExitError](err); ok {
		toolName := "other"
		if cmdName := cmdAsName(toolExecErr.Cmd); cmdName != "" {
			toolName = cmdName
		}
		return fmt.Sprintf("tool.%s.failed", toolName), []attribute.KeyValue{
			fields.ToolExitCode.Int(toolExecErr.ExitCode),
			fields.ToolName.String(toolName),
		}
	}
	if toolCheckErr, ok := errors.AsType[*tools.MissingToolErrors](err); ok {
		if len(toolCheckErr.ToolNames) == 1 {
			toolName := toolCheckErr.ToolNames[0]
			return fmt.Sprintf("tool.%s.missing", toolName),
				[]attribute.KeyValue{fields.ToolName.String(toolName)}
		}
		return "tool.multiple.missing", []attribute.KeyValue{
			fields.ToolName.String(strings.Join(toolCheckErr.ToolNames, ",")),
		}
	}
	if authFailedErr, ok := errors.AsType[*auth.AuthFailedError](err); ok {
		return "service.aad.failed", authFailedTelemetryDetails(authFailedErr)
	}
	if errors.Is(err, auth.ErrNoCurrentUser) {
		return "auth.not_logged_in", []attribute.KeyValue{fields.ErrCategory.String("auth")}
	}
	if _, ok := errors.AsType[*azidentity.AuthenticationFailedError](err); ok {
		return "auth.identity_failed", []attribute.KeyValue{fields.ErrCategory.String("auth")}
	}
	if code := classifySentinel(err); code != "" {
		return code, nil
	}
	if isNetworkError(err) {
		return "internal.network", []attribute.KeyValue{
			fields.ErrType.String(errchain.DeepestNamedType(err)),
		}
	}

	// Generic fallback: walk the chain and keep the deepest non-generic
	// type so wrappers like *fmt.wrapError or *errorhandler.ErrorWithSuggestion
	// don't mask the real error. internal.unclassified only fires
	// when no chain entry has a meaningful named type.
	deepest := errchain.DeepestNamedType(err)
	if deepest == "" || errchain.IsGenericWrapper(deepest) {
		return "internal.unclassified", []attribute.KeyValue{fields.ErrType.String(deepest)}
	}
	return fmt.Sprintf("internal.%s", errchain.SanitizeTypeName(deepest)),
		[]attribute.KeyValue{fields.ErrType.String(deepest)}
}

// classifyErrorWithSuggestion handles *internal.ErrorWithSuggestion.
// It preserves the historical narrow attribute set (only error.type
// from the inner classification, plus the auth special case) and
// emits the legacy `error.suggestion` ResultCode.
func classifyErrorWithSuggestion(
	ews *internal.ErrorWithSuggestion,
) (string, []attribute.KeyValue) {
	innerErr := ews.Unwrap()
	innerCode, _ := classify(innerErr)

	attrs := []attribute.KeyValue{fields.ErrType.String(innerCode)}

	// Preserve the AAD-detail enrichment when an AuthFailedError is
	// wrapped by a suggestion so it still surfaces on the outer span.
	if authFailedErr, ok := errors.AsType[*auth.AuthFailedError](innerErr); ok {
		attrs = append(attrs, authFailedTelemetryDetails(authFailedErr)...)
	}

	return "error.suggestion", attrs
}

func classifyResponseError(respErr *azcore.ResponseError) (string, []attribute.KeyValue) {
	serviceName := "other"
	statusCode := -1
	attrs := []attribute.KeyValue{fields.ServiceErrorCode.String(respErr.ErrorCode)}

	if respErr.RawResponse != nil {
		statusCode = respErr.RawResponse.StatusCode
		attrs = append(attrs, fields.ServiceStatusCode.Int(statusCode))

		if respErr.RawResponse.Request != nil {
			var hostName string
			serviceName, hostName = mapService(respErr.RawResponse.Request.Host)
			attrs = append(attrs,
				fields.ServiceHost.String(hostName),
				fields.ServiceMethod.String(respErr.RawResponse.Request.Method),
				fields.ServiceName.String(serviceName),
			)
		}
	}

	return fmt.Sprintf("service.%s.%d", serviceName, statusCode), attrs
}

func classifyArmDeployError(armDeployErr *azapi.AzureDeploymentError) (string, []attribute.KeyValue) {
	attrs := []attribute.KeyValue{fields.ServiceName.String("arm")}
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
			attrs = append(attrs, fields.ServiceErrorCode.String(string(codesJson)))
		}
	}

	// Use operation-specific error code if available
	operation := armDeployErr.Operation
	if operation == azapi.DeploymentOperationDeploy {
		// use 'deployment' instead of 'deploy' for consistency with prior naming
		operation = "deployment"
	}
	return fmt.Sprintf("service.arm.%s.failed", operation), attrs
}

func classifyExtServiceError(extServiceErr *azdext.ServiceError) (string, []attribute.KeyValue) {
	// Handle structured service errors from extensions.
	// Emit whatever details are available rather than requiring all fields.
	serviceName := ""
	var attrs []attribute.KeyValue
	if extServiceErr.ServiceName != "" {
		var hostDomain string
		serviceName, hostDomain = mapService(extServiceErr.ServiceName)
		attrs = append(attrs,
			fields.ServiceName.String(serviceName),
			fields.ServiceHost.String(hostDomain),
		)
	}
	if extServiceErr.StatusCode > 0 {
		attrs = append(attrs, fields.ServiceStatusCode.Int(extServiceErr.StatusCode))
	}
	if extServiceErr.ErrorCode != "" {
		attrs = append(attrs, fields.ServiceErrorCode.String(extServiceErr.ErrorCode))
	}

	// Use operation.errorCode (e.g. "ext.service.start_container.invalid_payload") for actionable
	// classification instead of host.statusCode which groups unrelated failures together.
	switch {
	case extServiceErr.ErrorCode != "":
		return fmt.Sprintf("ext.service.%s", normalizeCodeSegment(extServiceErr.ErrorCode, "failed")), attrs
	case extServiceErr.StatusCode > 0 && serviceName != "":
		return fmt.Sprintf("ext.service.%s.%d", serviceName, extServiceErr.StatusCode), attrs
	case extServiceErr.StatusCode > 0:
		return fmt.Sprintf("ext.service.unknown.%d", extServiceErr.StatusCode), attrs
	default:
		return "ext.service.unknown.failed", attrs
	}
}

func classifyExtLocalError(extLocalErr *azdext.LocalError) (string, []attribute.KeyValue) {
	domain := string(azdext.NormalizeLocalErrorCategory(extLocalErr.Category))
	code := normalizeCodeSegment(extLocalErr.Code, "failed")
	return fmt.Sprintf("ext.%s.%s", domain, code), []attribute.KeyValue{
		fields.ErrCategory.String(domain),
		fields.ErrCode.String(code),
	}
}

func authFailedTelemetryDetails(authFailedErr *auth.AuthFailedError) []attribute.KeyValue {
	errDetails := []attribute.KeyValue{fields.ServiceName.String("aad")}
	if authFailedErr == nil || authFailedErr.Parsed == nil {
		return errDetails
	}

	codes := make([]string, 0, len(authFailedErr.Parsed.ErrorCodes))
	for _, code := range authFailedErr.Parsed.ErrorCodes {
		codes = append(codes, fmt.Sprintf("%d", code))
	}

	return append(errDetails,
		fields.ServiceStatusCode.String(authFailedErr.Parsed.Error),
		fields.ServiceErrorCode.String(strings.Join(codes, ",")),
		fields.ServiceCorrelationId.String(authFailedErr.Parsed.CorrelationId),
	)
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
	case errors.Is(err, internal.ErrAbortedByUser):
		return "internal.operation_aborted"
	case errors.Is(err, terminal.InterruptErr),
		errors.Is(err, context.Canceled):
		return "user.canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "internal.timeout"
	case errors.Is(err, consent.ErrToolExecutionDenied):
		return "user.tool_denied"
	case errors.Is(err, git.ErrNotRepository):
		return "internal.not_git_repo"
	case errors.Is(err, azapi.ErrPreviewNotSupported):
		return "internal.preview_not_supported"
	case errors.Is(err, azapi.ErrCancelNotSupported):
		return "internal.cancel_not_supported"
	case errors.Is(err, provisioning.ErrBindMountOperationDisabled):
		return "internal.bind_mount_disabled"
	case errors.Is(err, provisioning.ErrDeploymentInterruptedLeaveRunning):
		return "user.canceled.leave_running"
	case errors.Is(err, provisioning.ErrDeploymentCanceledByUser):
		return "user.canceled.deployment_canceled"
	case errors.Is(err, provisioning.ErrDeploymentCancelTimeout):
		return "user.canceled.cancel_timed_out"
	case errors.Is(err, provisioning.ErrDeploymentCancelTooLate):
		return "user.canceled.cancel_too_late"
	case errors.Is(err, provisioning.ErrDeploymentCancelFailed):
		return "user.canceled.cancel_failed"
	case errors.Is(err, update.ErrNeedsElevation):
		return "update.elevationRequired"
	case errors.Is(err, pipeline.ErrRemoteHostIsNotAzDo):
		return "internal.remote_not_azdo"
	case errors.Is(err, internal.ErrToolUpgradeFailed):
		return "internal.tool_upgrade_failed"
	default:
		return ""
	}
}

// deploymentErrorCode is the per-frame ARM error breakdown that gets
// JSON-encoded into the service.errorCode telemetry property.
//
// "error.arm.frame_index" is an integer encoding the depth of the
// current line within the ARM deployment-error tree, not a Go runtime
// frame. It used to be named "error.frame", which conflicted with the
// (now removed) fields.ErrFrame attribute key.
//
// Field declaration order matches the alphabetical order of the json
// tags so encoding/json output matches map[string]any output in tests.
type deploymentErrorCode struct {
	Frame int    `json:"error.arm.frame_index"`
	Code  string `json:"error.code"`
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
