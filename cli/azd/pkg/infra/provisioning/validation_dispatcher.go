// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// ValidationCheckDispatcher dispatches validation checks to extension-provided
// check implementations. This interface decouples provisioning providers from
// the gRPC server implementation.
type ValidationCheckDispatcher interface {
	// DispatchChecks invokes all extension-registered checks matching the
	// given checkType and returns the aggregated results along with the
	// list of rule IDs that were invoked (for telemetry).
	DispatchChecks(
		ctx context.Context,
		checkType string,
		contextData map[string][]byte,
	) (results []*azdext.ValidationCheckResult, invokedRuleIDs []string, err error)
}
