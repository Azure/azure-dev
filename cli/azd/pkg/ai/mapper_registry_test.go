// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAiModelSkuToProto_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		src  AiModelSku
	}{
		{
			name: "fully populated",
			src: AiModelSku{
				Name:            "GlobalStandard",
				UsageName:       "OpenAI.GlobalStandard.gpt-4o",
				DefaultCapacity: 10,
				MinCapacity:     1,
				MaxCapacity:     100,
				CapacityStep:    5,
			},
		},
		{
			name: "zero values",
			src: AiModelSku{
				Name:            "",
				UsageName:       "",
				DefaultCapacity: 0,
				MinCapacity:     0,
				MaxCapacity:     0,
				CapacityStep:    0,
			},
		},
		{
			name: "large capacity",
			src: AiModelSku{
				Name:            "ProvisionedManaged",
				UsageName:       "OpenAI.ProvisionedManaged",
				DefaultCapacity: 1000,
				MinCapacity:     100,
				MaxCapacity:     10000,
				CapacityStep:    100,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto := aiModelSkuToProto(&tt.src)
			require.NotNil(t, proto)
			roundTripped := protoToAiModelSku(proto)
			require.NotNil(t, roundTripped)
			assert.Equal(t, tt.src, *roundTripped)
		})
	}
}

func TestProtoToAiModelSku(t *testing.T) {
	proto := &azdext.AiModelSku{
		Name:            "Standard",
		UsageName:       "OpenAI.Standard.gpt-4o",
		DefaultCapacity: 25,
		MinCapacity:     1,
		MaxCapacity:     200,
		CapacityStep:    1,
	}

	result := protoToAiModelSku(proto)
	require.NotNil(t, result)
	assert.Equal(t, "Standard", result.Name)
	assert.Equal(t, "OpenAI.Standard.gpt-4o", result.UsageName)
	assert.Equal(t, int32(25), result.DefaultCapacity)
	assert.Equal(t, int32(1), result.MinCapacity)
	assert.Equal(t, int32(200), result.MaxCapacity)
	assert.Equal(t, int32(1), result.CapacityStep)
}

func TestAiModelVersionToProto_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		src  AiModelVersion
	}{
		{
			name: "default version with skus",
			src: AiModelVersion{
				Version:   "2024-05-13",
				IsDefault: true,
				Skus: []AiModelSku{
					{
						Name:            "Standard",
						UsageName:       "OpenAI.Standard.gpt-4o",
						DefaultCapacity: 10,
						MinCapacity:     1,
						MaxCapacity:     100,
						CapacityStep:    1,
					},
				},
			},
		},
		{
			name: "non-default version without skus",
			src: AiModelVersion{
				Version:   "1.0",
				IsDefault: false,
				Skus:      []AiModelSku{},
			},
		},
		{
			name: "multiple skus",
			src: AiModelVersion{
				Version:   "v2",
				IsDefault: false,
				Skus: []AiModelSku{
					{
						Name:      "Standard",
						UsageName: "usage-a",
					},
					{
						Name:      "GlobalStandard",
						UsageName: "usage-b",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proto, err := aiModelVersionToProto(&tt.src)
			require.NoError(t, err)
			require.NotNil(t, proto)

			roundTripped := protoToAiModelVersion(proto)

			assert.Equal(t, tt.src.Version, roundTripped.Version)
			assert.Equal(t, tt.src.IsDefault, roundTripped.IsDefault)
			require.Len(t, roundTripped.Skus, len(tt.src.Skus))

			for i, sku := range tt.src.Skus {
				assert.Equal(t, sku, roundTripped.Skus[i])
			}
		})
	}
}

func TestAiModelSkuToProto_FieldMapping(t *testing.T) {
	src := &AiModelSku{
		Name:            "TestSku",
		UsageName:       "Test.Usage.Name",
		DefaultCapacity: 42,
		MinCapacity:     5,
		MaxCapacity:     500,
		CapacityStep:    10,
	}

	proto := aiModelSkuToProto(src)

	assert.Equal(t, "TestSku", proto.Name)
	assert.Equal(t, "Test.Usage.Name", proto.UsageName)
	assert.Equal(t, int32(42), proto.DefaultCapacity)
	assert.Equal(t, int32(5), proto.MinCapacity)
	assert.Equal(t, int32(500), proto.MaxCapacity)
	assert.Equal(t, int32(10), proto.CapacityStep)
}
