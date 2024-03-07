package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/AlecAivazis/survey/v2/terminal"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// MapError maps the given error to a telemetry span, setting relevant status and attributes.
func MapError(err error, span tracing.Span) {
	errCode := "UnknownError"
	var errDetails []attribute.KeyValue

	var respErr *azcore.ResponseError
	var armDeployErr *azapi.AzureDeploymentError
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
	} else if errors.Is(err, terminal.InterruptErr) {
		errCode = "user.canceled"
	}

	if len(errDetails) > 0 {
		for i, detail := range errDetails {
			errDetails[i].Key = fields.ErrorKey(detail.Key)
		}

		span.SetAttributes(errDetails...)
	}

	span.SetStatus(codes.Error, errCode)
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
