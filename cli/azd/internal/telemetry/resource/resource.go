// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package resource provides application-level resource attributes for telemetry purposes.
package resource

import (
	"runtime"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil/osversion"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"
)

// New creates a resource with all application-level fields populated.
func New() *resource.Resource {
	r, _ := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			fields.ServiceNameKey.String(fields.ServiceNameAzd),
			fields.ServiceVersionKey.String(internal.GetVersionNumber()),
			fields.OSTypeKey.String(runtime.GOOS),
			fields.OSVersionKey.String(getOsVersion()),
			fields.HostArchKey.String(runtime.GOARCH),
			fields.ProcessRuntimeVersionKey.String(runtime.Version()),
			fields.ExecutionEnvironmentKey.String(getExecutionEnvironment()),
			fields.MachineIdKey.String(getMachineId()),
		),
	)

	return r
}

func getOsVersion() string {
	ver, err := osversion.GetVersion()

	if err != nil {
		return "Unknown"
	}

	return ver
}
