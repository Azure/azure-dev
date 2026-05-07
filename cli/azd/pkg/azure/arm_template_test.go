// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_TargetScope(t *testing.T) {
	tests := []struct {
		name    string
		schema  string
		want    DeploymentScope
		wantErr bool
	}{
		{
			name: "SubscriptionScope",
			schema: "https://schema.management.azure.com/" +
				"schemas/2018-05-01/" +
				"subscriptionDeploymentTemplate.json#",
			want: DeploymentScopeSubscription,
		},
		{
			name: "ResourceGroupScope",
			schema: "https://schema.management.azure.com/" +
				"schemas/2019-04-01/" +
				"deploymentTemplate.json#",
			want: DeploymentScopeResourceGroup,
		},
		{
			name: "ResourceGroupCaseInsensitive",
			schema: "https://schema.management.azure.com/" +
				"schemas/2019-04-01/" +
				"DeploymentTemplate.json#",
			want: DeploymentScopeResourceGroup,
		},
		{
			name:    "EmptySchema",
			schema:  "",
			wantErr: true,
		},
		{
			name:    "UnknownSchema",
			schema:  "https://example.com/unknown.json",
			wantErr: true,
		},
		{
			name:    "InvalidURL",
			schema:  "://bad-url",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl := ArmTemplate{Schema: tt.schema}
			got, err := tmpl.TargetScope()

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func Test_IsSecuredARMType(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		want bool
	}{
		{"SecureString", "securestring", true},
		{"SecureObject", "secureobject", true},
		{"SecureStringUpper", "SecureString", true},
		{"SecureObjectMixed", "SecureObject", true},
		{"AllCaps", "SECURESTRING", true},
		{"String", "string", false},
		{"Object", "object", false},
		{"Int", "int", false},
		{"Bool", "bool", false},
		{"Empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsSecuredARMType(tt.typ)
			require.Equal(t, tt.want, got)
		})
	}
}

func Test_Secure(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		want bool
	}{
		{"SecureString", "securestring", true},
		{"RegularString", "string", false},
		{"SecureObject", "secureobject", true},
		{"RegularObject", "object", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			param := ArmTemplateParameterDefinition{
				Type: tt.typ,
			}
			require.Equal(t, tt.want, param.Secure())
		})
	}
}

func Test_Description(t *testing.T) {
	tests := []struct {
		name   string
		meta   map[string]json.RawMessage
		want   string
		wantOK bool
	}{
		{
			name: "WithDescription",
			meta: map[string]json.RawMessage{
				"description": json.RawMessage(
					`"A test parameter"`,
				),
			},
			want:   "A test parameter",
			wantOK: true,
		},
		{
			name:   "NoMetadata",
			meta:   nil,
			want:   "",
			wantOK: false,
		},
		{
			name: "NoDescriptionKey",
			meta: map[string]json.RawMessage{
				"other": json.RawMessage(`"something"`),
			},
			want:   "",
			wantOK: false,
		},
		{
			name: "InvalidDescriptionJSON",
			meta: map[string]json.RawMessage{
				"description": json.RawMessage(`123`),
			},
			want:   "",
			wantOK: false,
		},
		{
			name: "EmptyDescription",
			meta: map[string]json.RawMessage{
				"description": json.RawMessage(`""`),
			},
			want:   "",
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			param := ArmTemplateParameterDefinition{
				Metadata: tt.meta,
			}
			got, ok := param.Description()
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func Test_AzdMetadata(t *testing.T) {
	locationType := AzdMetadataTypeLocation

	tests := []struct {
		name   string
		meta   map[string]json.RawMessage
		wantOK bool
		check  func(t *testing.T, m AzdMetadata)
	}{
		{
			name: "WithLocationMetadata",
			meta: map[string]json.RawMessage{
				"azd": json.RawMessage(
					`{"type":"location"}`,
				),
			},
			wantOK: true,
			check: func(t *testing.T, m AzdMetadata) {
				require.NotNil(t, m.Type)
				require.Equal(t, locationType, *m.Type)
			},
		},
		{
			name:   "NoMetadata",
			meta:   nil,
			wantOK: false,
			check:  func(t *testing.T, m AzdMetadata) {},
		},
		{
			name: "NoAzdKey",
			meta: map[string]json.RawMessage{
				"other": json.RawMessage(`{}`),
			},
			wantOK: false,
			check:  func(t *testing.T, m AzdMetadata) {},
		},
		{
			name: "InvalidAzdJSON",
			meta: map[string]json.RawMessage{
				"azd": json.RawMessage(`not-valid`),
			},
			wantOK: false,
			check:  func(t *testing.T, m AzdMetadata) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			param := ArmTemplateParameterDefinition{
				Metadata: tt.meta,
			}
			got, ok := param.AzdMetadata()
			require.Equal(t, tt.wantOK, ok)
			tt.check(t, got)
		})
	}
}

func Test_AdditionalProperties_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		hasProps bool
		wantErr  bool
	}{
		{
			name:     "FalseValue",
			input:    "false",
			hasProps: false,
		},
		{
			name:     "ObjectValue",
			input:    `{"type":"string"}`,
			hasProps: true,
		},
		{
			name:     "ObjectWithMinMax",
			input:    `{"type":"int","minValue":1,"maxValue":10}`,
			hasProps: true,
		},
		{
			name:    "InvalidJSON",
			input:   `{bad json}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v ArmTemplateParameterAdditionalPropertiesValue
			err := v.UnmarshalJSON([]byte(tt.input))

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.hasProps, v.HasAdditionalProperties())

			if tt.hasProps {
				props := v.Properties()
				require.NotEmpty(t, props.Type)
			}
		})
	}
}

func Test_AdditionalProperties_MarshalJSON(t *testing.T) {
	t.Run("NilProps", func(t *testing.T) {
		v := ArmTemplateParameterAdditionalPropertiesValue{}
		data, err := v.MarshalJSON()
		require.NoError(t, err)
		require.Equal(t, "false", string(data))
	})

	t.Run("WithProps", func(t *testing.T) {
		v := ArmTemplateParameterAdditionalPropertiesValue{}
		err := v.UnmarshalJSON(
			[]byte(`{"type":"string"}`),
		)
		require.NoError(t, err)

		data, err := v.MarshalJSON()
		require.NoError(t, err)

		var parsed map[string]any
		err = json.Unmarshal(data, &parsed)
		require.NoError(t, err)
		require.Equal(t, "string", parsed["type"])
	})
}

func Test_AdditionalProperties_RoundTrip(t *testing.T) {
	original := `{"type":"object"}`
	var v ArmTemplateParameterAdditionalPropertiesValue
	err := v.UnmarshalJSON([]byte(original))
	require.NoError(t, err)

	data, err := v.MarshalJSON()
	require.NoError(t, err)

	var v2 ArmTemplateParameterAdditionalPropertiesValue
	err = v2.UnmarshalJSON(data)
	require.NoError(t, err)
	require.True(t, v2.HasAdditionalProperties())
	require.Equal(t, "object", v2.Properties().Type)
}

func Test_UsageName_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		json string
		want []string
	}{
		{
			name: "SingleString",
			json: `{"usageName": "foo"}`,
			want: []string{"foo"},
		},
		{
			name: "ArrayOfStrings",
			json: `{"usageName": ["foo", "bar"]}`,
			want: []string{"foo", "bar"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m AzdMetadata
			err := json.Unmarshal([]byte(tt.json), &m)
			require.NoError(t, err)
			require.Equal(t, tt.want, []string(m.UsageName))
		})
	}
}
