import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_ConfigOptions_SecretsAndVars(t *testing.T) {
	// Initialize the ConfigOptions instance
	config := &ConfigOptions{
		Variables:                    []string{"var1", "var2"},
		Secrets:                      []string{"secret1", "secret2"},
		AdditionalVariablesAsSecrets: true,
	}

	// Define the initial variables, secrets, and environment
	initialVariables := map[string]string{
		"var1": "value1",
	}
	initialSecrets := map[string]string{
		"secret1": "value2",
	}
	env := map[string]string{
		"var1":    "new_value1",
		"var2":    "value2",
		"secret1": "new_value2",
		"secret2": "value3",
	}

	// Call the SecretsAndVars function
	variables, secrets := config.SecretsAndVars(initialVariables, initialSecrets, env)

	// Assert the expected results
	expectedVariables := map[string]string{
		"var1": "new_value1",
		"var2": "value2",
	}
	expectedSecrets := map[string]string{
		"secret1": "new_value2",
		"secret2": "value3",
	}
	assert.Equal(t, expectedVariables, variables)
	assert.Equal(t, expectedSecrets, secrets)
}