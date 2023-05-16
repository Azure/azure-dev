package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azdo"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/spf13/pflag"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// Telemetry middleware tracks telemetry for the given action
type TelemetryMiddleware struct {
	options *Options
}

// Creates a new Telemetry middleware instance
func NewTelemetryMiddleware(options *Options) Middleware {
	return &TelemetryMiddleware{
		options: options,
	}
}

// Invokes the middleware and wraps the action with a telemetry span for telemetry reporting
func (m *TelemetryMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	// Note: CommandPath is constructed using the Use member on each command up to the root.
	// It does not contain user input, and is safe for telemetry emission.
	cmdPath := events.GetCommandEventName(m.options.CommandPath)
	spanCtx, span := tracing.Start(ctx, cmdPath)

	log.Printf("TraceID: %s", span.SpanContext().TraceID())

	if !m.options.IsChildAction() {
		// Set the command name as a baggage item on the span context.
		// This allow inner actions to have command name attached.
		spanCtx = tracing.SetBaggageInContext(
			spanCtx,
			fields.CmdEntry.String(cmdPath))
	}

	if m.options.Flags != nil {
		changedFlags := []string{}
		m.options.Flags.VisitAll(func(f *pflag.Flag) {
			if f.Changed {
				changedFlags = append(changedFlags, f.Name)
			}
		})
		span.SetAttributes(fields.CmdFlags.StringSlice(changedFlags))
	}

	span.SetAttributes(fields.CmdArgsCount.Int(len(m.options.Args)))

	defer func() {
		// Include any usage attributes set
		span.SetAttributes(tracing.GetUsageAttributes()...)
		span.End()
	}()

	result, err := next(spanCtx)
	if result == nil {
		result = &actions.ActionResult{}
	}
	result.TraceID = span.SpanContext().TraceID().String()

	if err != nil {
		mapError(err, span)
	}

	return result, err
}

func mapError(err error, span tracing.Span) {
	errCode := "UnknownError"
	var errDetails []attribute.KeyValue

	var respErr *azcore.ResponseError
	var armDeployErr *azcli.AzureDeploymentError
	var toolExecErr *exec.ExitError
	var authFailedErr *auth.AuthFailedError
	if errors.As(err, &respErr) {
		serviceName := "other"
		statusCode := -1
		errDetails = append(errDetails, fields.ServiceErrorCode.String(respErr.ErrorCode))

		if respErr.RawResponse != nil {
			statusCode = respErr.RawResponse.StatusCode
			errDetails = append(errDetails, fields.ServiceStatusCode.Int(statusCode))

			if respErr.RawResponse.Request != nil {
				serviceName = mapServiceName(respErr.RawResponse.Request.Host)
				errDetails = append(errDetails,
					fields.ServiceHost.String(respErr.RawResponse.Request.Host),
					fields.ServiceMethod.String(respErr.RawResponse.Request.Method),
					fields.ServiceName.String(serviceName),
				)
			}
		}

		errCode = fmt.Sprintf("service.%s.%d", serviceName, statusCode)
	} else if errors.As(err, &armDeployErr) {
		errDetails = append(errDetails, fields.ServiceName.String("arm"))
		codes := []*deploymentErrorCode{}
		var collect func(details []*azcli.DeploymentErrorLine, frame int)
		collect = func(details []*azcli.DeploymentErrorLine, frame int) {
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

		collect([]*azcli.DeploymentErrorLine{armDeployErr.Details}, 0)
		if len(codes) > 0 {
			errDetails = append(errDetails, fields.ServiceErrorCode.String(codes[0].Code))
			codes = codes[1:]
		}

		if len(codes) > 0 {
			if inner, err := json.Marshal(codes); err != nil {
				log.Println("telemetry: failed to marshal inner error", err)
			} else {
				errDetails = append(errDetails, fields.ErrInner.String(string(inner)))
			}
		}

		errCode = "service.arm.deployment.failed"
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
	}

	if len(errDetails) > 0 {
		errDetailsMap := make(map[string]interface{}, len(errDetails))
		for _, detail := range errDetails {
			errDetailsMap[string(detail.Key)] = detail.Value.AsInterface()
		}

		if errDetailsStr, err := json.Marshal(errDetailsMap); err != nil {
			log.Println("telemetry: failed to marshal error details", err)
		} else {
			span.SetAttributes(fields.ErrDetails.String(string(errDetailsStr)))
		}
	}

	span.SetStatus(codes.Error, errCode)
}

type deploymentErrorCode struct {
	Code  string `json:"error.code"`
	Frame int    `json:"error.frame"`
}

func collectCode(lines []*azcli.DeploymentErrorLine, frame int) *deploymentErrorCode {
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

func mapServiceName(host string) string {
	name := "other"

	switch host {
	case azdo.AzDoHostName:
		name = "azdo"
	case azure.ManagementHostName:
		name = "arm"
	case graphsdk.HostName:
		name = "graph"
	}

	return name
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
