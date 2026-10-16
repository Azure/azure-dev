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
