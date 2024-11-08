package kubectl

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/braydonk/yaml"
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
	t.Run("json", func(t *testing.T) {
		var ingressResources List[Ingress]
		ingressBytes, err := os.ReadFile("../../../test/testdata/k8s/parse/ingress.json")
		require.NoError(t, err)

		err = json.Unmarshal(ingressBytes, &ingressResources)
		require.NoError(t, err)

		require.Equal(t, "myapp.centralus.cloudapp.azure.com", *ingressResources.Items[0].Spec.Rules[0].Host)
		require.Equal(t, "myapp.centralus.cloudapp.azure.com", ingressResources.Items[0].Spec.Tls[0].Hosts[0])
	})

	t.Run("yaml", func(t *testing.T) {
		var ingressResources List[Ingress]
		ingressBytes, err := os.ReadFile("../../../test/testdata/k8s/parse/ingress.yaml")
		require.NoError(t, err)

		err = yaml.Unmarshal(ingressBytes, &ingressResources)
		require.NoError(t, err)

		require.Equal(t, "myapp.centralus.cloudapp.azure.com", *ingressResources.Items[0].Spec.Rules[0].Host)
		require.Equal(t, "myapp.centralus.cloudapp.azure.com", ingressResources.Items[0].Spec.Tls[0].Hosts[0])
	})
}
