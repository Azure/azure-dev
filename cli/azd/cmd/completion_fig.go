// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/internal/figspec"
	"github.com/spf13/cobra"
)

// FigSpec wraps the figspec.Spec for easier access
type FigSpec = figspec.Spec

// FigGenerator wraps the figspec.Generator
type FigGenerator struct {
	*figspec.Generator
}

// NewFigGenerator creates a new Fig spec generator
func NewFigGenerator(includeHidden bool) *FigGenerator {
	return &FigGenerator{
		Generator: figspec.NewGenerator(includeHidden),
	}
}

// GenerateSpec generates a Fig spec from a Cobra root command
func (g *FigGenerator) GenerateSpec(root *cobra.Command) *FigSpec {
	return g.Generator.GenerateSpec(root)
}
