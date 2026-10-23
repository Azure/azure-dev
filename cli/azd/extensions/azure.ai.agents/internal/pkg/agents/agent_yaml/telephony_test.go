// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestVoiceTelephonyBindingJSONPreservesOmittedVersusEmptyTargets(t *testing.T) {
	base := VoiceTelephonyBinding{Provider: "twilio", Identifier: "+14255550123", Connection: "connection"}

	omitted, err := json.Marshal(base)
	require.NoError(t, err)
	require.JSONEq(t,
		`{"provider":"twilio","identifier":"+14255550123","connection":"connection"}`,
		string(omitted))

	base.TransferTargets = []map[string]any{}
	explicitEmpty, err := json.Marshal(base)
	require.NoError(t, err)
	require.JSONEq(t,
		`{"provider":"twilio","identifier":"+14255550123","connection":"connection","transferTargets":[]}`,
		string(explicitEmpty))
}

func TestVoiceTelephonyBindingYAMLDecodesExplicitEmptyTargets(t *testing.T) {
	var binding VoiceTelephonyBinding
	require.NoError(t, yaml.Unmarshal([]byte(`
provider: twilio
identifier: "+14255550123"
connection: connection
transfer_targets: []
`), &binding))
	require.NotNil(t, binding.TransferTargets)
	require.Empty(t, binding.TransferTargets)
}

func TestVoiceTelephonyBindingJSONRejectsSnakeCaseTargets(t *testing.T) {
	var binding VoiceTelephonyBinding
	err := json.Unmarshal([]byte(`{
		"provider":"twilio","identifier":"+14255550123","connection":"connection",
		"transfer_targets":[]
	}`), &binding)
	require.ErrorContains(t, err, "use transferTargets")
}

func TestVoiceTelephonyBindingJSONPreservesExplicitEmptyTargets(t *testing.T) {
	var binding VoiceTelephonyBinding
	require.NoError(t, json.Unmarshal([]byte(`{
		"provider":"twilio","identifier":"+14255550123","connection":"connection",
		"transferTargets":[]
	}`), &binding))
	require.NotNil(t, binding.TransferTargets)
	require.Empty(t, binding.TransferTargets)
}

func TestValidateVoiceTelephonyTransferTargets(t *testing.T) {
	valid := func() *VoiceTelephony {
		return &VoiceTelephony{Bindings: []VoiceTelephonyBinding{{
			Provider: "twilio", Identifier: "+14255550123", Connection: "connection",
			TransferTargets: []map[string]any{{
				"name": "support", "description": "Support",
				"destination": map[string]any{
					"kind": "teams", "value": "28:orgid:00000000-0000-0000-0000-000000000001",
				},
			}},
		}}}
	}
	require.NoError(t, ValidateVoiceTelephony(valid()))

	tests := []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{name: "unknown target field", edit: func(target map[string]any) { target["future"] = true }, want: "unsupported field"},
		{name: "unknown destination field", edit: func(target map[string]any) {
			target["destination"].(map[string]any)["future"] = true
		}, want: "unsupported field"},
		{name: "invalid teams GUID", edit: func(target map[string]any) {
			target["destination"].(map[string]any)["value"] = "not-a-guid"
		}, want: "canonical object GUID"},
		{name: "invalid PSTN", edit: func(target map[string]any) {
			destination := target["destination"].(map[string]any)
			destination["kind"], destination["value"] = "pstn", "4255550123"
		}, want: "+<E.164>"},
		{name: "relative SIP", edit: func(target map[string]any) {
			destination := target["destination"].(map[string]any)
			destination["kind"], destination["value"] = "sip", "relative/path"
		}, want: "user@host"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			telephony := valid()
			test.edit(telephony.Bindings[0].TransferTargets[0])
			require.ErrorContains(t, ValidateVoiceTelephony(telephony), test.want)
		})
	}

	duplicate := valid()
	duplicate.Bindings[0].TransferTargets = append(duplicate.Bindings[0].TransferTargets,
		map[string]any{
			"name": "support", "description": "Other",
			"destination": map[string]any{"kind": "pstn", "value": "+14255550124"},
		})
	require.ErrorContains(t, ValidateVoiceTelephony(duplicate), "duplicates transfer target")
}
