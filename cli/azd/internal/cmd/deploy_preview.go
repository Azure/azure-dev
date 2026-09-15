// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

// DeploymentPreviewResult contains read-only deployment previews for the selected services.
type DeploymentPreviewResult struct {
	Timestamp       time.Time                                      `json:"timestamp"`
	Services        map[string]*project.ServiceDeployPreviewResult `json:"services"`
	SkippedServices map[string]string                              `json:"skippedServices,omitempty"`
}

type unsupportedDeploymentPreviewError struct {
	service string
	host    project.ServiceTargetKind
}

func (e *unsupportedDeploymentPreviewError) Error() string {
	return fmt.Sprintf("service host '%s' for service '%s' does not support deployment preview", e.host, e.service)
}

func (da *DeployAction) preview(ctx context.Context, targetServiceName string) (*actions.ActionResult, error) {
	if da.flags.fromPackage != "" || (da.flags.flagSet != nil && da.flags.flagSet.Changed("from-package")) {
		return nil, errors.New("cannot specify both --preview and --from-package")
	}
	if da.flags.All && targetServiceName != "" {
		return nil, errors.New("cannot specify both --all and <service>")
	}

	timeout, err := da.resolveDeployTimeout()
	if err != nil {
		return nil, err
	}

	services, err := da.previewServices(targetServiceName)
	if err != nil {
		return nil, err
	}

	result := DeploymentPreviewResult{
		Services: make(map[string]*project.ServiceDeployPreviewResult, len(services)),
	}
	var unsupported *unsupportedDeploymentPreviewError
	for _, svc := range services {
		preview, err := da.previewService(ctx, svc, timeout)
		if err != nil {
			if skipped, ok := errors.AsType[*unsupportedDeploymentPreviewError](err); ok {
				unsupported = skipped
				if result.SkippedServices == nil {
					result.SkippedServices = map[string]string{}
				}
				result.SkippedServices[svc.Name] = string(svc.Host)
				continue
			}
			return nil, err
		}
		result.Services[svc.Name] = preview
	}
	if len(result.Services) == 0 && unsupported != nil {
		return nil, fmt.Errorf("no selected service could be previewed: %w", unsupported)
	}
	result.Timestamp = time.Now()

	if da.formatter.Kind() == output.JsonFormat {
		if err := da.formatter.Format(result, da.writer, nil); err != nil {
			return nil, fmt.Errorf("deployment preview could not be displayed: %w", err)
		}
	} else {
		for _, svc := range services {
			preview, exists := result.Services[svc.Name]
			if !exists {
				continue
			}
			message := preview.Message
			if message != "" {
				if !strings.HasSuffix(message, "\n") {
					message += "\n"
				}
				if _, err := fmt.Fprint(da.writer, message); err != nil {
					return nil, fmt.Errorf("deployment preview could not be displayed: %w", err)
				}
			}
		}
	}

	return &actions.ActionResult{}, nil
}

func (da *DeployAction) previewServices(targetServiceName string) ([]*project.ServiceConfig, error) {
	// The regular service selector imports Aspire services and may build an AppHost.
	// Preview resolves only declared services, without importing or initializing them.
	services, err := da.declaredServiceResolver.ServiceStableDeclared(da.projectConfig)
	if err != nil {
		return nil, err
	}

	if targetServiceName == "" && !da.flags.All {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		projectDirectory := da.projectConfig.Path
		if da.azdCtx != nil {
			projectDirectory = da.azdCtx.ProjectDirectory()
		}
		if wd != projectDirectory {
			for _, svc := range services {
				if wd == svc.Path() {
					targetServiceName = svc.Name
					break
				}
			}
			if targetServiceName == "" {
				return nil, errors.New(
					"current working directory is not a project or declared service directory. " +
						"Specify a service name to preview a service, or specify --all to preview all declared services. " +
						"Deployment preview does not import generated services",
				)
			}
		}
	}

	if targetServiceName != "" {
		if _, exists := da.projectConfig.Services[targetServiceName]; !exists {
			return nil, fmt.Errorf(
				"service name '%s' is not declared in azure.yaml. Deployment preview does not import generated services",
				targetServiceName,
			)
		}
	}

	services, err = project.FilterServicesByCondition(services, targetServiceName, da.env.Getenv)
	if err != nil {
		return nil, err
	}
	for _, svc := range services {
		if svc.DotNetContainerApp != nil || svc.BuildOnly {
			return nil, fmt.Errorf("deployment preview is not supported for generated service '%s'", svc.Name)
		}
	}
	return services, nil
}

func (da *DeployAction) previewService(
	ctx context.Context,
	service *project.ServiceConfig,
	timeout time.Duration,
) (*project.ServiceDeployPreviewResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	target, err := da.serviceTargetResolver.GetServiceTarget(ctx, service)
	if err != nil {
		return nil, fmt.Errorf("resolving service host for preview of '%s': %w", service.Name, err)
	}
	previewer, ok := target.(project.ServiceTargetPreviewer)
	capability, hasCapability := target.(project.ServiceTargetPreviewCapability)
	if !ok || (hasCapability && !capability.SupportsPreview()) {
		return nil, &unsupportedDeploymentPreviewError{service: service.Name, host: service.Host}
	}

	if da.formatter.Kind() != output.JsonFormat {
		message := fmt.Sprintf("Comparing configuration for service: %s", sanitizeServiceName(service.Name))
		da.console.ShowSpinner(ctx, message, input.Step)
		defer da.console.StopSpinner(ctx, "", input.Step)
	}

	result, err := previewer.Preview(ctx, service)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("previewing deployment of service '%s': %w", service.Name, err)
	}
	if result == nil {
		return nil, fmt.Errorf("invalid deployment preview for service '%s': provider returned no result", service.Name)
	}
	return result, nil
}
