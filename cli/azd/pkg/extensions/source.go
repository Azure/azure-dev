// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

type Source interface {
	// Name returns the name of the source.
	Name() string
	// ListTemplates returns a list of AZD compatible templates.
	ListExtensions(ctx context.Context) ([]*ExtensionMetadata, error)
	// GetTemplate returns a template by path.
	GetExtension(ctx context.Context, name string) (*ExtensionMetadata, error)
}

type registrySource struct {
	name     string
	registry *Registry
}

// newRegistrySource creates a new registry source.
func newRegistrySource(name string, registry *Registry) (Source, error) {
	return &registrySource{
		name:     name,
		registry: registry,
	}, nil
}

func (ts *registrySource) Name() string {
	return ts.name
}

// ListTemplates returns a list of templates from the extension source.
func (s *registrySource) ListExtensions(ctx context.Context) ([]*ExtensionMetadata, error) {
	for _, extension := range s.registry.Extensions {
		extension.Source = s.name
	}

	return s.registry.Extensions, nil
}

// GetExtension returns an extension by id.
func (s *registrySource) GetExtension(ctx context.Context, id string) (*ExtensionMetadata, error) {
	allTemplates, err := s.ListExtensions(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed listing templates: %w", err)
	}

	matchingIndex := slices.IndexFunc(allTemplates, func(extension *ExtensionMetadata) bool {
		return strings.EqualFold(extension.Id, id)
	})

	if matchingIndex == -1 {
		return nil, fmt.Errorf("'%s' %w", id, ErrRegistryExtensionNotFound)
	}

	return allTemplates[matchingIndex], nil
}
