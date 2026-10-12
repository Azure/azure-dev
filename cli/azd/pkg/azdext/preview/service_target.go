// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package preview contains experimental extension SDK capabilities backed by v1beta.
// These APIs may change before graduating to the stable SDK.
package preview

import (
	"context"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
)

// ServiceTargetPreviewProvider optionally compares desired and deployed state without deployment.
// Implement it alongside azdext.ServiceTargetProvider and register through
// ExtensionHost.WithBetaServiceTargetPreview. Preview runs on a fresh provider without Initialize.
// It must not build, package, publish, deploy, or persist deployment state.
type ServiceTargetPreviewProvider interface {
	Preview(context.Context, *v1beta.ServiceConfig) (*v1beta.ServiceDeployPreviewResult, error)
}
