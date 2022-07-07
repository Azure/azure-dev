package internal

import (
	"fmt"
	"os"
	"strings"
)

// FormatUserAgent formats the user agent with its base information (azd/<version>), information
// that's included from the environment (`AZURE_DEV_USER_AGENT`) and any extra text passed
// via 'extra'.
func FormatUserAgent(extras []string) string {
	// like the Azure CLI (via it's `AZURE_HTTP_USER_AGENT` env variable) we allow for a user to append
	// information to the UserAgent by setting an environment variable.
	devUserAgent := os.Getenv("AZURE_DEV_USER_AGENT")

	if devUserAgent != "" {
		devUserAgent = " " + devUserAgent
	}

	var appendUA string

	if len(extras) > 0 {
		appendUA = " " + strings.Join(extras, " ")
	}

	// and by default we always include azdev and our version number.
	// Ex: AZURECLI/2.34.1 (DEB) <other things...> azdev/0.0.0-alpha.0 azdtempl/functestapp@v1
	return fmt.Sprintf("azdev/%s%s%s", GetVersionNumber(), devUserAgent, appendUA)
}

// FormatTemplateForUserAgent formats the template into a UserAgent value.
func FormatTemplateForUserAgent(template string) string {
	if template == "" {
		template = "[none]"
	}
	return fmt.Sprintf("azdtempl/%s", template)
}
