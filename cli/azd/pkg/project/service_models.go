package project

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

// Some endpoints include a discriminator suffix that should be displayed instead of the default 'Endpoint' label.
var endpointPattern = regexp.MustCompile(`(.+):\s(.+)`)

// ServiceLifecycleEventArgs are the event arguments available when
// any service lifecycle event has been triggered
type ServiceLifecycleEventArgs struct {
	Project *ProjectConfig
	Service *ServiceConfig
	Args    map[string]any
}

// ServiceProgress represents an incremental progress message
// during a service operation such as restore, build, package & deploy
type ServiceProgress struct {
	Message   string
	Timestamp time.Time
}

// NewServiceProgress is a helper method to create a new
// progress message with a current timestamp
func NewServiceProgress(message string) ServiceProgress {
	return ServiceProgress{
		Message:   message,
		Timestamp: time.Now(),
	}
}

// ServiceRestoreResult is the result of a successful Restore operation
type ServiceRestoreResult struct {
	Details interface{} `json:"details"`
}

// ServiceBuildResult is the result of a successful Build operation
type ServiceBuildResult struct {
	Restore         *ServiceRestoreResult `json:"restore"`
	BuildOutputPath string                `json:"buildOutputPath"`
	Details         interface{}           `json:"details"`
}

// Supports rendering messages for UX items
func (sbr *ServiceBuildResult) ToString(currentIndentation string) string {
	uxItem, ok := sbr.Details.(ux.UxItem)
	if ok {
		return uxItem.ToString(currentIndentation)
	}

	return fmt.Sprintf("%s- Build Output: %s", currentIndentation, output.WithLinkFormat(sbr.BuildOutputPath))
}

func (sbr *ServiceBuildResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(*sbr)
}

type PackageOptions struct {
	OutputPath string
}

// ServicePackageResult is the result of a successful Package operation
type ServicePackageResult struct {
	Build       *ServiceBuildResult `json:"build"`
	PackagePath string              `json:"packagePath"`
	Details     interface{}         `json:"details"`
}

// Supports rendering messages for UX items
func (spr *ServicePackageResult) ToString(currentIndentation string) string {
	uxItem, ok := spr.Details.(ux.UxItem)
	if ok {
		return uxItem.ToString(currentIndentation)
	}

	if spr.PackagePath != "" {
		return fmt.Sprintf("%s- Package Output: %s", currentIndentation, output.WithLinkFormat(spr.PackagePath))
	}

	return ""
}

func (spr *ServicePackageResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(*spr)
}

// ServiceDeployResult is the result of a successful Deploy operation
type ServiceDeployResult struct {
	Package *ServicePackageResult `json:"package"`
	// Related Azure resource ID
	TargetResourceId string            `json:"targetResourceId"`
	Kind             ServiceTargetKind `json:"kind"`
	Endpoints        []string          `json:"endpoints"`
	Details          interface{}       `json:"details"`
}

// Supports rendering messages for UX items
func (spr *ServiceDeployResult) ToString(currentIndentation string) string {
	uxItem, ok := spr.Details.(ux.UxItem)
	if ok {
		return uxItem.ToString(currentIndentation)
	}

	builder := strings.Builder{}

	if len(spr.Endpoints) == 0 {
		builder.WriteString(fmt.Sprintf("%s- No endpoints were found\n", currentIndentation))
	} else {
		for _, endpoint := range spr.Endpoints {
			label := "Endpoint"
			url := endpoint

			// When the endpoint pattern is matched used the first sub match as the endpoint label.
			matches := endpointPattern.FindStringSubmatch(endpoint)
			if len(matches) == 3 {
				label = matches[1]
				url = matches[2]
			}

			builder.WriteString(fmt.Sprintf("%s- %s: %s\n", currentIndentation, label, output.WithLinkFormat(url)))
		}
	}

	return builder.String()
}

func (spr *ServiceDeployResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(*spr)
}
