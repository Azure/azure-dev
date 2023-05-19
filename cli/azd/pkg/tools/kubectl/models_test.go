package kubectl

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_Port_TargetPort_Unmarshalling(t *testing.T) {
	tests := map[string]struct {
		input    string
		expected string
	}{
		"StringValue": {
			input:    "{ \"port\": 80, \"protocol\": \"http\", \"targetPort\": \"redis\" }",
			expected: "redis",
		},
		"IntValue": {
			input:    "{ \"port\": 80, \"protocol\": \"http\", \"targetPort\": 6379 }",
			expected: "6379",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var port Port
			err := json.Unmarshal([]byte(test.input), &port)
			require.NoError(t, err)
			require.Equal(t, test.expected, fmt.Sprint(port.TargetPort))
		})
	}
}
