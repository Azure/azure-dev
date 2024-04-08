package recording

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"gopkg.in/dnaeon/go-vcr.v3/cassette"
)

func sanitizeContainerAppTokenExchange(i *cassette.Interaction) error {
	if strings.HasSuffix(i.Request.Host, "azurecr.io") && strings.HasSuffix(i.Request.URL, "/oauth2/exchange") {
		i.Request.Form.Set("access_token", "SANITIZED")
		tokenRegexp := regexp.MustCompile("(access_token=)(.*?)(&|$)")
		i.Request.Body = tokenRegexp.ReplaceAllString(i.Request.Body, "${1}SANITIZED${3}")

		body := map[string]any{}
		err := json.Unmarshal([]byte(i.Response.Body), &body)
		if err != nil {
			return fmt.Errorf("unmarshalling oauth2/exchange: %w", err)
		}

		body["refresh_token"] = "SANITIZED"
		sanitized, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshaling oauth2/exchange: %w", err)
		}

		i.Response.Body = string(sanitized)
	}

	return nil
}

func sanitizeContainerAppListSecrets(i *cassette.Interaction) error {
	if i.Request.Method == "POST" &&
		// TODO: Pull from config
		i.Request.Host == "management.azure.com" &&
		strings.Contains(i.Request.URL, "/Microsoft.App/containerApps") &&
		strings.Contains(i.Request.URL, "/listSecrets") {
		body := armappcontainers.ContainerAppsClientListSecretsResponse{}
		err := json.Unmarshal([]byte(i.Response.Body), &body)
		if err != nil {
			return fmt.Errorf("unmarshalling Microsoft.App/containerApps listSecrets: %w", err)
		}

		for i := range body.Value {
			if body.Value[i].Name != nil {
				if body.Value[i].Value != nil {
					val := *body.Value[i].Value
					// Redis requirepass. Sanitize the password, remove other config.
					if strings.Contains(val, "requirepass ") {
						body.Value[i].Value = convert.RefOf("requirepass SANITIZED")
					}
					continue
				}

				body.Value[i].Value = convert.RefOf("SANITIZED")
			}
		}

		sanitized, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshaling Microsoft.App/containerApps listSecrets: %w", err)
		}

		i.Response.Body = string(sanitized)
	}

	return nil
}

func sanitizeContainerAppUpdate(i *cassette.Interaction) error {
	if i.Request.Method == "PATCH" || i.Request.Method == "POST" &&
		// TODO: Pull this from config
		i.Request.Host == "management.azure.com" &&
		strings.Contains(i.Request.URL, "/Microsoft.App/containerApps/") {
		split := strings.Split(i.Request.URL, "/Microsoft.App/containerApps/")
		if strings.Contains(split[1], "/") {
			// This is a containerApps sub-resource level operation
			return nil
		}

		ca := armappcontainers.ContainerApp{}
		err := json.Unmarshal([]byte(i.Request.Body), &ca)
		if err != nil {
			return fmt.Errorf("unmarshalling Microsoft.App/containerApps request: %w", err)
		}

		if ca.Properties != nil &&
			ca.Properties.Configuration != nil &&
			ca.Properties.Configuration.Secrets != nil {
			for i := range ca.Properties.Configuration.Secrets {
				if ca.Properties.Configuration.Secrets[i] != nil {
					ca.Properties.Configuration.Secrets[i].Value = convert.RefOf("SANITIZED")
				}
			}
		}

		sanitized, err := json.Marshal(ca)
		if err != nil {
			return fmt.Errorf("marshaling Microsoft.App/containerApps request: %w", err)
		}

		i.Request.Body = string(sanitized)
	}
	return nil
}
