// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// doc_index.go implements `azd ai doc` (the bare front-door command).
// The catalog data (which sibling ai.* extensions ship docs) lives in
// doc_catalog.go; the rendering lives in doc_renderer.go. runDocIndex
// is the thin RunE that prints the rendered body + examples.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// runDocIndex is the RunE for the bare `azd ai doc` command. Prints
// the styled root catalog body (preamble + Available Documentation +
// per-category links) followed by the styled Examples block. Same
// content shows in `azd ai doc --help` because root.go wires the two
// renderers into helpformat.Install's Description and Footer slots.
func runDocIndex(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprint(out, renderRootBody(docCategories)); err != nil {
		return err
	}
	if _, err := fmt.Fprint(out, renderRootExamples(docCategories)); err != nil {
		return err
	}
	return nil
}
