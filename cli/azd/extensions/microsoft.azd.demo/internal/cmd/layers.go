// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

type layersClient interface {
	GetLayer(context.Context, *v1beta.GetLayerRequest, ...grpc.CallOption) (*v1beta.LayerResponse, error)
	ListLayers(context.Context, *v1beta.EmptyRequest, ...grpc.CallOption) (*v1beta.ListLayersResponse, error)
}

func newLayersCommand() *cobra.Command {
	var expandEnv bool

	cmd := &cobra.Command{
		Use:   "layers [name]",
		Short: "List project layers or show one layer.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if expandEnv && len(args) == 0 {
				return errors.New("--expand-env requires a layer name")
			}

			ctx := azdext.WithAccessToken(cmd.Context())
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
					return nil
				}
				return fmt.Errorf("failed waiting for debugger: %w", err)
			}

			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return runLayers(ctx, azdClient.BetaProject(), cmd.OutOrStdout(), name, expandEnv)
		},
	}

	cmd.Flags().BoolVar(&expandEnv, "expand-env", false, "Expand environment references in the selected layer")
	return cmd
}

func runLayers(ctx context.Context, client layersClient, writer io.Writer, name string, expandEnv bool) error {
	if name != "" {
		response, err := client.GetLayer(ctx, &v1beta.GetLayerRequest{Name: name, Envsubst: expandEnv})
		if err != nil {
			return fmt.Errorf("failed to get layer %q: %w", name, err)
		}
		return writeLayer(writer, response.GetLayer())
	}

	response, err := client.ListLayers(ctx, &v1beta.EmptyRequest{})
	if err != nil {
		return fmt.Errorf("failed to list project layers: %w", err)
	}
	if len(response.GetLayers()) == 0 {
		_, err := fmt.Fprintln(writer, "No project layers found.")
		return err
	}

	for index, layer := range response.GetLayers() {
		if index > 0 {
			if _, err := fmt.Fprintln(writer); err != nil {
				return err
			}
		}
		if err := writeLayer(writer, layer); err != nil {
			return err
		}
	}
	return nil
}

func writeLayer(writer io.Writer, layer *v1beta.Layer) error {
	if layer == nil {
		return errors.New("layer response did not include a layer")
	}

	dependsOn := "none"
	if len(layer.GetDependsOn()) > 0 {
		dependsOn = strings.Join(layer.GetDependsOn(), ", ")
	}
	services := slices.Sorted(maps.Keys(layer.GetServices()))
	serviceNames := "none"
	if len(services) > 0 {
		serviceNames = strings.Join(services, ", ")
	}

	if _, err := fmt.Fprintf(writer, "Layer: %s\n", layer.GetName()); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "  Depends on: %s\n", dependsOn); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "  Services: %s\n", serviceNames); err != nil {
		return err
	}
	if len(layer.GetInfra()) == 0 {
		_, err := fmt.Fprintln(writer, "  Infrastructure: none")
		return err
	}

	if _, err := fmt.Fprintln(writer, "  Infrastructure:"); err != nil {
		return err
	}
	for _, infra := range layer.GetInfra() {
		if _, err := fmt.Fprintf(writer, "    - %s (%s)\n", infra.GetProvider(), infra.GetPath()); err != nil {
			return err
		}
	}
	return nil
}
