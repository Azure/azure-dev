package project

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
)

// ServiceLifecycleEventArgs are the event arguments available when
// any service lifecycle event has been triggered
type ServiceLifecycleEventArgs struct {
	Project *ProjectConfig
	Service *ServiceConfig
	Args    map[string]any
}

// ServiceProgress represents an incremental progress message
// during a service operation such as restore, build, package, publish & deploy
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
	return json.Marshal(sbr)
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

	return fmt.Sprintf("%s- Package Output: %s", currentIndentation, output.WithLinkFormat(spr.PackagePath))
}

func (spr *ServicePackageResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(spr)
}

// ServicePublishResult is the result of a successful Publish operation
type ServicePublishResult struct {
	Package *ServicePackageResult `json:"package"`
	// Related Azure resource ID
	TargetResourceId string            `json:"targetResourceId"`
	Kind             ServiceTargetKind `json:"kind"`
	Endpoints        []string          `json:"endpoints"`
	Details          interface{}       `json:"details"`
}

// Supports rendering messages for UX items
func (spr *ServicePublishResult) ToString(currentIndentation string) string {
	uxItem, ok := spr.Details.(ux.UxItem)
	if ok {
		return uxItem.ToString(currentIndentation)
	}

	builder := strings.Builder{}

	for _, endpoint := range spr.Endpoints {
		builder.WriteString(fmt.Sprintf("%s- Endpoint: %s\n", currentIndentation, output.WithLinkFormat(endpoint)))
	}

	return builder.String()
}

func (spr *ServicePublishResult) MarshalJSON() ([]byte, error) {
	return json.Marshal(spr)
}

// ServiceDeployResult is the result of a successful Deploy operation
type ServiceDeployResult struct {
	Restore *ServiceRestoreResult `json:"restore"`
	Build   *ServiceBuildResult   `json:"build"`
	Package *ServicePackageResult `json:"package"`
	Publish *ServicePublishResult `json:"publish"`
	Details interface{}           `json:"details"`
}
