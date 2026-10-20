// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package preview retains deprecated extension SDK capabilities backed by v1beta.
// Deployment preview graduated to the stable azdext package in azd 1.37.0.
package preview

import (
	"context"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
)

// ServiceTargetPreviewProvider optionally compares desired and deployed state without deployment.
// Implement it alongside azdext.ServiceTargetProvider and register through
// ExtensionHost.WithBetaServiceTargetPreview. Preview runs on a fresh provider without Initialize.
// It must not build, package, publish, deploy, or persist deployment state.
//
// Deprecated: use azdext.ServiceTargetPreviewProvider with stable types in azd 1.37.0 or later.
type ServiceTargetPreviewProvider interface {
	//nolint:staticcheck // Preserve the deprecated beta provider's source-compatible signature.
	Preview(context.Context, *v1beta.ServiceConfig) (*v1beta.ServiceDeployPreviewResult, error)
}
