// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

var projectServiceUpdateMu sync.Mutex

// AddServiceSerialized prevents concurrent azure.yaml writes.
func AddServiceSerialized(
	ctx context.Context,
	client *azdext.AzdClient,
	service *azdext.ServiceConfig,
) error {
	projectServiceUpdateMu.Lock()
	defer projectServiceUpdateMu.Unlock()

	_, err := client.Project().AddService(
		ctx,
		&azdext.AddServiceRequest{Service: service},
	)
	return err
}
