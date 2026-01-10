// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/mapper"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func init() {
	registerEnvironmentMappings()
}

// registerEnvironmentMappings registers all environment type conversions with the mapper.
// This allows other packages to convert environment types to proto types via the mapper.
func registerEnvironmentMappings() {
	// TargetResource -> proto TargetResource conversion
	mapper.MustRegister(func(ctx context.Context, src *TargetResource) (*azdext.TargetResource, error) {
		if src == nil {
			return nil, nil
		}

		protoTarget := &azdext.TargetResource{
			SubscriptionId:    src.SubscriptionId(),
			ResourceGroupName: src.ResourceGroupName(),
			ResourceName:      src.ResourceName(),
			ResourceType:      src.ResourceType(),
		}

		if metadata := src.Metadata(); metadata != nil {
			protoTarget.Metadata = metadata
		}

		return protoTarget, nil
	})
}
