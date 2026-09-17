// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type fakeLayersClient struct {
	getLayer   func(context.Context, *v1beta.GetLayerRequest) (*v1beta.LayerResponse, error)
	listLayers func(context.Context, *v1beta.EmptyRequest) (*v1beta.ListLayersResponse, error)
}

func (c *fakeLayersClient) GetLayer(
	ctx context.Context,
	request *v1beta.GetLayerRequest,
	_ ...grpc.CallOption,
) (*v1beta.LayerResponse, error) {
	return c.getLayer(ctx, request)
}

func (c *fakeLayersClient) ListLayers(
	ctx context.Context,
	request *v1beta.EmptyRequest,
	_ ...grpc.CallOption,
) (*v1beta.ListLayersResponse, error) {
	return c.listLayers(ctx, request)
}

func TestRunLayersListsLayers(t *testing.T) {
	client := &fakeLayersClient{
		listLayers: func(context.Context, *v1beta.EmptyRequest) (*v1beta.ListLayersResponse, error) {
			return &v1beta.ListLayersResponse{Layers: []*v1beta.Layer{
				{
					Name:      "application",
					DependsOn: []string{"foundation"},
					Services: map[string]*v1beta.ServiceConfig{
						"web": {},
						"api": {},
					},
					Infra: []*v1beta.InfraOptions{{Provider: "bicep", Path: "infra/app"}},
				},
			}}, nil
		},
	}

	var output bytes.Buffer
	err := runLayers(t.Context(), client, &output, "", false)

	require.NoError(t, err)
	require.Equal(t, "Layer: application\n"+
		"  Depends on: foundation\n"+
		"  Services: api, web\n"+
		"  Infrastructure:\n"+
		"    - bicep (infra/app)\n", output.String())
}

func TestRunLayersGetsLayerWithExpandedEnvironment(t *testing.T) {
	client := &fakeLayersClient{
		getLayer: func(_ context.Context, request *v1beta.GetLayerRequest) (*v1beta.LayerResponse, error) {
			require.Equal(t, "application", request.GetName())
			require.True(t, request.GetEnvsubst())
			return &v1beta.LayerResponse{Layer: &v1beta.Layer{Name: request.GetName()}}, nil
		},
	}

	var output bytes.Buffer
	err := runLayers(t.Context(), client, &output, "application", true)

	require.NoError(t, err)
	require.Contains(t, output.String(), "Layer: application")
}

func TestRunLayersWrapsListError(t *testing.T) {
	client := &fakeLayersClient{
		listLayers: func(context.Context, *v1beta.EmptyRequest) (*v1beta.ListLayersResponse, error) {
			return nil, errors.New("rpc unavailable")
		},
	}

	err := runLayers(t.Context(), client, &bytes.Buffer{}, "", false)

	require.EqualError(t, err, "failed to list project layers: rpc unavailable")
}

func TestLayersCommandRejectsExpandEnvWithoutName(t *testing.T) {
	command := newLayersCommand()
	command.SetArgs([]string{"--expand-env"})

	err := command.ExecuteContext(t.Context())

	require.EqualError(t, err, "--expand-env requires a layer name")
}
