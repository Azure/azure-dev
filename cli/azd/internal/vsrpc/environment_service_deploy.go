// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
)

// DeployAsync is the server implementation of:
// ValueTask<Environment> DeployAsync(RequestContext, string, IObserver<ProgressMessage>, CancellationToken)
//
// While it is named simply `DeployAsync`, it behaves as if the user had run `azd provision` and `azd deploy`.
func (s *environmentService) DeployAsync(
	ctx context.Context, rc RequestContext, name string, observer *Observer[ProgressMessage],
) (*Environment, error) {
	return s.DeployServiceAsync(ctx, rc, name, "", observer)
}
