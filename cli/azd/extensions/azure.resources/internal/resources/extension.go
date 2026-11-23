// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import "context"

// GenerateRequest identifies an infrastructure entry and its resource configuration.
type GenerateRequest struct {
	Name   string
	Config map[string]any
}

// GenerateFromConfig validates the entry's raw resource configuration and returns its Bicep sources.
func GenerateFromConfig(ctx context.Context, request GenerateRequest) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, err := Decode(request.Config)
	if err != nil {
		return nil, err
	}
	return Generate(request.Name, config)
}
