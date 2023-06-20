package exec

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactSensitiveData(t *testing.T) {
	tests := []struct {
		scenario string
		input    string
		expected string
	}{
		{scenario: "Basic",
			input: `"accessToken": "eyJ0eX",
"expiresOn": "2022-08-11 10:33:39.000000",
"subscription": "2cd61",
"tenant": "72f988bf",
"tokenType": "Bearer"
}`,
			expected: `"accessToken": "<redacted>",
"expiresOn": "2022-08-11 10:33:39.000000",
"subscription": "2cd61",
"tenant": "72f988bf",
"tokenType": "Bearer"
}`},
		{scenario: "NoReplacement",
			input: `"expiresOn": "2022-08-11 10:33:39.000000",
"subscription": "2cd61",
"tenant": "72f988bf",
"tokenType": "Bearer"
}`,
			expected: `"expiresOn": "2022-08-11 10:33:39.000000",
"subscription": "2cd61",
"tenant": "72f988bf",
"tokenType": "Bearer"
}`},
		{scenario: "MultipleReplacement",
			input: `"accessToken": "eyJ0eX",
"expiresOn": "2022-08-11 10:33:39.000000",
"subscription": "2cd61",
"tenant": "72f988bf",
"tokenType": "Bearer",
"accessToken": "skJ02wsfK"
}`,
			expected: `"accessToken": "<redacted>",
"expiresOn": "2022-08-11 10:33:39.000000",
"subscription": "2cd61",
"tenant": "72f988bf",
"tokenType": "Bearer",
"accessToken": "<redacted>"
}`},

		{scenario: "SWADeploymentToken",
			// nolint:lll
			input: `npx -y @azure/static-web-apps-cli@1.0.6 deploy --tenant-id abc-123 --subscription-id abc-123 --resource-group r --app-name app-name --app-location / --output-location . --env default --no-use-keychain --deployment-token abc-123`,
			// nolint:lll
			expected: `npx -y @azure/static-web-apps-cli@1.0.6 deploy --tenant-id abc-123 --subscription-id abc-123 --resource-group r --app-name app-name --app-location / --output-location . --env default --no-use-keychain --deployment-token <redacted>`},

		{scenario: "DockerLoginUsernameAndPassword",
			input:    `docker login --username crusername123 --password abc123 some.azurecr.io`,
			expected: `docker login --username <redacted> --password <redacted> some.azurecr.io`},
	}

	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			actual := RedactSensitiveData(test.input)
			require.Equal(t, test.expected, actual)
		})
	}
}

func TestSanitizingLogWriter_Write(t *testing.T) {
	data := [][]byte{
		[]byte("Authenticating...\nOutput:\n  {\"accessToken\":\"0123abcd\"}\r\n"), // 56 bytes
		[]byte("Logging:\ncmd --username username123 --deployment-token 0123abcd"), // 63 bytes
		[]byte("\n\r\n"), // 3 bytes
	}

	out := new(bytes.Buffer)
	writer := &sanitizingLogWriter{
		w: out,
	}

	var total int
	for _, p := range data {
		written, err := writer.Write(p)
		require.NoError(t, err)
		total += written
	}

	const expected = "   Authenticating...\n   Output:\n     {\"accessToken\":\"<redacted>\"}\n   \n   Logging:\n   cmd --username <redacted> --deployment-token <redacted>\n   \n   \n   \n"
	require.Equal(t, expected, out.String())
	require.Equal(t, 56+63+3, total)
}
