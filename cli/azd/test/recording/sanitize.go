package recording

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
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
						body.Value[i].Value = to.Ptr("requirepass SANITIZED")
						continue
					}
				}

				body.Value[i].Value = to.Ptr("SANITIZED")
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

func sanitizeContainerRegistryListBuildSourceUploadUrl(i *cassette.Interaction) error {
	if i.Request.Method == "POST" &&
		// TODO: Pull from config
		i.Request.Host == "management.azure.com" &&
		strings.Contains(i.Request.URL, "/Microsoft.ContainerRegistry/registries") &&
		strings.Contains(i.Request.URL, "/listBuildSourceUploadUrl") {
		body := armcontainerregistry.SourceUploadDefinition{}
		err := json.Unmarshal([]byte(i.Response.Body), &body)
		if err != nil {
			return fmt.Errorf("unmarshalling Microsoft.ContainerRegistry/registries listBuildSourceUploadUrl: %w", err)
		}

		s, err := sanitizeQueryParameter(*body.UploadURL, "sig")
		if err != nil {
			return fmt.Errorf("sanitizing UploadURL: %w", err)
		}
		body.UploadURL = to.Ptr(s)

		sanitized, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshaling Microsoft.ContainerRegistry/registries listBuildSourceUploadUrl: %w", err)
		}

		i.Response.Body = string(sanitized)
	}

	return nil
}

func sanitizeContainerRegistryListLogSasUrl(i *cassette.Interaction) error {
	if i.Request.Method == "POST" &&
		// TODO: Pull from config
		i.Request.Host == "management.azure.com" &&
		strings.Contains(i.Request.URL, "/Microsoft.ContainerRegistry/registries") &&
		strings.Contains(i.Request.URL, "/listLogSasUrl") {
		body := armcontainerregistry.RunsClientGetLogSasURLResponse{}
		err := json.Unmarshal([]byte(i.Response.Body), &body)
		if err != nil {
			return fmt.Errorf("unmarshalling Microsoft.ContainerRegistry/registries listLogSasUrl: %w", err)
		}

		s, err := sanitizeQueryParameter(*body.LogLink, "sig")
		if err != nil {
			return fmt.Errorf("sanitizing UploadURL: %w", err)
		}
		body.LogLink = to.Ptr(s)

		sanitized, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshaling Microsoft.ContainerRegistry/registries listLogSasUrl: %w", err)
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
					ca.Properties.Configuration.Secrets[i].Value = to.Ptr("SANITIZED")
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

func sanitizeBlobStorageSasSig(i *cassette.Interaction) error {
	// TODO: Pull this from config
	if strings.HasSuffix(strings.ToLower(i.Request.Host), "blob.core.windows.net") {
		sanitized, err := sanitizeQueryParameter(i.Request.URL, "sig")
		if err != nil {
			return fmt.Errorf("sanitizing sig parameter: %w", err)
		}

		i.Request.URL = sanitized
	}

	return nil
}

// sanitizeQueryParameter replaces the value of the query parameter with the given key in the given URL with "SANITIZED".
func sanitizeQueryParameter(u string, key string) (string, error) {
	parsed, err := url.Parse(u)
	if err != nil {
		return "", fmt.Errorf("parsing blob URL: %w", err)
	}

	query := parsed.Query()

	if !query.Has(key) {
		return u, nil
	}

	query.Set(key, "SANITIZED")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
