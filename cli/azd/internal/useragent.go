package internal

import (
	"fmt"
	"os"
	"strings"
)

// MakeUserAgent creates a user agent string that contains all necessary product identifiers, in increasing order:
// - The Azure Developer CLI version, formatted as `azdev/<version>`
// - The user agent set by the user, from `AZURE_DEV_USER_AGENT` environment variable
// - Any additional product identifiers specified in `additionalProductIdentifiers`.
func MakeUserAgent(additionalProductIdentifiers []string) string {
	// like the Azure CLI (via it's `AZURE_HTTP_USER_AGENT` env variable) we allow for a user to append
	// information to the UserAgent by setting an environment variable.
	devUserAgent := os.Getenv("AZURE_DEV_USER_AGENT")

	if devUserAgent != "" {
		devUserAgent = " " + devUserAgent
	}

	var remainingProductIdentifiers string

	if len(additionalProductIdentifiers) > 0 {
		remainingProductIdentifiers = " " + strings.Join(additionalProductIdentifiers, " ")
	}

	// and by default we always include azdev and our version number.
	// Ex: AZURECLI/2.34.1 (DEB) <other things...> azdev/0.0.0-alpha.0 azdtempl/functestapp@v1
	return fmt.Sprintf("azdev/%s%s%s", GetVersionNumber(), devUserAgent, remainingProductIdentifiers)
}

// FormatTemplateAsProductIdentifier formats the template as a user-agent product identifier.
func FormatTemplateAsProductIdentifier(template string) string {
	if template == "" {
		template = "[none]"
	}
	return fmt.Sprintf("azdtempl/%s", template)
}
