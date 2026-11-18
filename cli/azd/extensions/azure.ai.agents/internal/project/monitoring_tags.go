// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// monitoringTagsClient is the subset of armresources.TagsClient used to refresh the component.
type monitoringTagsClient interface {
	GetAtScope(
		ctx context.Context, scope string, options *armresources.TagsClientGetAtScopeOptions,
	) (armresources.TagsClientGetAtScopeResponse, error)
	UpdateAtScope(
		ctx context.Context, scope string, parameters armresources.TagsPatchResource,
		options *armresources.TagsClientUpdateAtScopeOptions,
	) (armresources.TagsClientUpdateAtScopeResponse, error)
}

// refreshMonitoringRoleSyncTag merges the sync tag onto the component. When the assignment already
// existed (created is false) it only writes the tag if it is missing, so repeat deploys are no-ops
// but an earlier failed refresh is retried.
func refreshMonitoringRoleSyncTag(
	ctx context.Context,
	tags monitoringTagsClient,
	componentID string,
	created bool,
	now time.Time,
) error {
	if !created {
		current, err := tags.GetAtScope(ctx, componentID, nil)
		if err != nil {
			return err
		}
		if current.Properties != nil && current.Properties.Tags != nil &&
			current.Properties.Tags[monitoringRoleSyncTag] != nil {
			return nil
		}
	}
	_, err := tags.UpdateAtScope(ctx, componentID, armresources.TagsPatchResource{
		Operation: new(armresources.TagsPatchOperationMerge),
		Properties: &armresources.Tags{Tags: map[string]*string{
			monitoringRoleSyncTag: new(now.UTC().Format(time.RFC3339)),
		}},
	}, nil)
	return err
}
