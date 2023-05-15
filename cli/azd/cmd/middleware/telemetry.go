package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
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
		errCode = "service.arm.deploymentFailed"
	} else if errors.As(err, &toolExecErr) {
		toolName := "other"
		cmdName := cleanCmd(toolExecErr.Cmd)
		if cmdName != "" {
			toolName = cmdName
		}

		errDetails = append(errDetails,
			fields.ToolExitCode.Int(toolExecErr.ExitCode),
			fields.ToolName.String(toolName))

		errCode = fmt.Sprintf("tool.%s.failed", toolName)
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

func mapServiceName(host string) string {
	name := "other"

	switch host {
	case "dev.azure.com":
		name = "azdo"
	case "management.azure.com":
		name = "arm"
	case "login.microsoftonline.com":
		name = "aad"
	case "graph.microsoft.com":
		name = "graph"
	}

	return name
}

func cleanCmd(cmd string) string {
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

	return cmd
}
