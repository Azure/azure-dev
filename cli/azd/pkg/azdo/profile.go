// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/google/uuid"
	"github.com/microsoft/azure-devops-go-api/azuredevops/profile"
)

func getProfileId(ctx context.Context, client profile.Client) (*uuid.UUID, error) {
	userProfile, err := client.GetProfile(ctx, profile.GetProfileArgs{
		Id: convert.RefOf("me"),
	})
	if err != nil {
		return nil, fmt.Errorf("getting user profile", err)
	}
	return userProfile.Id, nil
}
