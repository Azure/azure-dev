package azure

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsAutoGen(t *testing.T) {
	// Test case 1: No AzdMetadata
	param := ArmTemplateParameterDefinition{
		Type: "string",
	}
	result := param.IsAutoGen()
	require.False(t, result)

	// Test case 2: AzdMetadata.Type is nil
	m := make(map[string]json.RawMessage)
	m["azd"] = json.RawMessage(`{}`)
	param = ArmTemplateParameterDefinition{
		Type:     "string",
		Metadata: m,
	}
	result = param.IsAutoGen()
	require.False(t, result)

	// Test case 3: AzdMetadata.Type is not AzdMetadataTypeGenerate
	m["azd"] = json.RawMessage(`{"type": "foo"}`)
	param = ArmTemplateParameterDefinition{
		Type:     "string",
		Metadata: m,
	}
	result = param.IsAutoGen()
	require.False(t, result)

	// Test case 4: AzdMetadata.Type is AzdMetadataTypeGenerate
	m["azd"] = json.RawMessage(`{"type": "generate", "config": {"length": 10}}`)
	param = ArmTemplateParameterDefinition{
		Type:     "string",
		Metadata: m,
	}
	result = param.IsAutoGen()
	require.True(t, result)

	// Test case 5: Type is not string
	param = ArmTemplateParameterDefinition{
		Type:     "int",
		Metadata: m,
	}
	result = param.IsAutoGen()
	require.False(t, result)
}
