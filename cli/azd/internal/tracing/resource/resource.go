// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package resource provides application-level resource attributes for telemetry purposes.
package resource

import (
	"context"
	"fmt"
	"runtime"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil/osversion"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

// New creates the canonical resource for azd telemetry.
func New() *resource.Resource {
	r, err := resource.New(
		context.Background(),
		resource.WithTelemetrySDK(),
		resource.WithAttributes(
			fields.ServiceNameKey.String(fields.ServiceNameAzd),
			fields.ServiceVersionKey.String(internal.VersionInfo().Version.String()),
			fields.OSTypeKey.String(runtime.GOOS),
			fields.OSVersionKey.String(getOsVersion()),
			fields.HostArchKey.String(runtime.GOARCH),
			fields.ProcessRuntimeVersionKey.String(runtime.Version()),
			fields.ExecutionEnvironmentKey.String(getExecutionEnvironment()),
			fields.MachineIdKey.String(MachineId()),
			fields.InstalledByKey.String(getInstalledBy()),
			fields.DevDeviceIdKey.String(DevDeviceId()),
		),
		resource.WithSchemaURL(semconv.SchemaURL),
	)

	// One possible reason this might fail is if semconv.SchemaURL does not match the schema used by
	// resource.WithTelemetrySDK(). Fail eagerly instead of returning a resource without the expected attributes.
	if err != nil {
		panic(fmt.Sprintf("failed to create resource: %v", err))
	}

	return r
}

func getOsVersion() string {
	ver, err := osversion.GetVersion()

	if err != nil {
		return "Unknown"
	}

	return ver
}
