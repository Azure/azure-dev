// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package resources

import "context"

// Generator owns resource interpretation and synthesis in the resources extension.
// It owns resource interpretation and synthesis, but no provisioning authority.
type Generator struct{}

// GenerateRequest identifies an infrastructure entry and its resource configuration.
type GenerateRequest struct {
	Name   string
	Config map[string]any
}

// Generate validates the entry's resource configuration and returns its Bicep sources.
func (g *Generator) Generate(ctx context.Context, request GenerateRequest) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, err := Decode(request.Config)
	if err != nil {
		return nil, err
	}
	return Generate(request.Name, config)
}
