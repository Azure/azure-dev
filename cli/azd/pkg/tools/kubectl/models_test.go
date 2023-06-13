package kubectl

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Port_TargetPort_Unmarshalling(t *testing.T) {
	tests := map[string]struct {
		input       string
		expected    string
		expectError bool
	}{
		"StringValue": {
			input:    "{ \"port\": 80, \"protocol\": \"http\", \"targetPort\": \"redis\" }",
			expected: "redis",
		},
		"IntValue": {
			input:    "{ \"port\": 80, \"protocol\": \"http\", \"targetPort\": 6379 }",
			expected: "6379",
		},
		"InvalidType": {
			input:       "{ \"port\": 80, \"protocol\": \"http\", \"targetPort\": { \"foo\": \"bar\" } }",
			expectError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var port Port
			err := json.Unmarshal([]byte(test.input), &port)
			if test.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, test.expected, fmt.Sprint(port.TargetPort))
			require.Equal(t, 80, port.Port)
			require.Equal(t, "http", port.Protocol)
		})
	}
}

func Test_Ingress_UnMarshalling(t *testing.T) {
	var ingress Ingress
	ingressBytes, err := os.ReadFile("../../../test/testdata/k8s/ingress.json")
	require.NoError(t, err)

	err = json.Unmarshal(ingressBytes, &ingress)
	require.NoError(t, err)

	require.Equal(t, "myapp.centralus.cloudapp.azure.com", ingress.Spec.Rules[0].Host)
	require.Equal(t, "myapp.centralus.cloudapp.azure.com", ingress.Spec.Tls[0].Hosts[0])
}
