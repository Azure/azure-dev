package devcenter

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/stretchr/testify/require"
)

func Test_Map_Outputs(t *testing.T) {
	outputsResponse := &devcentersdk.OutputListResponse{
		Outputs: map[string]devcentersdk.OutputParameter{
			"test_Bool": {
				Type:      devcentersdk.OutputParameterTypeBoolean,
				Value:     true,
				Sensitive: false,
			},
			"test_String": {
				Type:      devcentersdk.OutputParameterTypeString,
				Value:     "test",
				Sensitive: false,
			},
			"test_Number": {
				Type:      devcentersdk.OutputParameterTypeNumber,
				Value:     11,
				Sensitive: false,
			},
			"test_Array": {
				Type:      devcentersdk.OutputParameterTypeArray,
				Value:     []interface{}{"test1", "test2"},
				Sensitive: false,
			},
			"test_Object": {
				Type:      devcentersdk.OutputParameterTypeObject,
				Value:     map[string]interface{}{"key1": "value1", "key2": "value2"},
				Sensitive: false,
			},
		},
	}

	outputs, err := createOutputParameters(outputsResponse)
	require.NoError(t, err)
	require.Len(t, outputs, len(outputsResponse.Outputs))

	require.Equal(t, provisioning.OutputParameter{
		Type:  provisioning.ParameterTypeBoolean,
		Value: true,
	}, outputs["TEST_BOOL"])
	require.Equal(t, provisioning.OutputParameter{
		Type:  provisioning.ParameterTypeString,
		Value: "test",
	}, outputs["TEST_STRING"])
	require.Equal(t, provisioning.OutputParameter{
		Type:  provisioning.ParameterTypeNumber,
		Value: 11,
	}, outputs["TEST_NUMBER"])
	require.Equal(t, provisioning.OutputParameter{
		Type:  provisioning.ParameterTypeArray,
		Value: []interface{}{"test1", "test2"},
	}, outputs["TEST_ARRAY"])
	require.Equal(t, provisioning.OutputParameter{
		Type:  provisioning.ParameterTypeObject,
		Value: map[string]interface{}{"key1": "value1", "key2": "value2"},
	}, outputs["TEST_OBJECT"])
}
