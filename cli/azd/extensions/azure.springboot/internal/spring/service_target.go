// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type serviceTarget struct {
	azdext.BaseServiceTargetProvider
	runtime *Runtime
}

// NewServiceTarget creates the initialize-only Spring Boot service target.
func NewServiceTarget(runtime *Runtime) azdext.ServiceTargetProvider {
	return &serviceTarget{runtime: runtime}
}

func (t *serviceTarget) Initialize(ctx context.Context, serviceConfig *azdext.ServiceConfig) error {
	return t.runtime.ValidateService(ctx, serviceConfig)
}
