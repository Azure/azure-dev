// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"bytes"
	"crypto/sha1" // #nosec G505 -- Git source blob identity, not a security primitive.
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const petclinicRestCommit = "427ef7f7e6d3244e5aabd101f5a73435320599fa"

type restSourceManifest struct {
	Repository    string            `json:"repository"`
	Commit        string            `json:"commit"`
	HashAlgorithm string            `json:"hashAlgorithm"`
	Files         map[string]string `json:"files"`
	Evidence      []string          `json:"evidence"`
	Overlays      []string          `json:"overlays"`
}

func petclinicRestFixturePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(azdSourcePath(t), "extensions", "azure.springboot",
		"test", "functional", "testdata", "spring-petclinic-rest-weidong")
}

func TestSpringPetclinicRestPinnedOverlay(t *testing.T) {
	root := petclinicRestFixturePath(t)
	var manifest restSourceManifest
	require.NoError(t, json.Unmarshal([]byte(readFixtureFile(t, root, "source-revision.json")), &manifest))
	require.Equal(t, "https://github.com/weidongxu-microsoft/petclinic-rest-azd", manifest.Repository)
	require.Equal(t, petclinicRestCommit, manifest.Commit)
	require.Equal(t, "git-blob-sha1", manifest.HashAlgorithm)
	require.Equal(t, []string{"azure.yaml"}, manifest.Overlays)
	require.Greater(t, len(manifest.Files), len(manifest.Evidence), "do not vendor the whole app")
	var actualEvidence []string
	require.NoError(t, filepath.WalkDir(filepath.Join(root, "upstream"),
		func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(filepath.Join(root, "upstream"), path)
			if err != nil {
				return err
			}
			actualEvidence = append(actualEvidence, filepath.ToSlash(relative))
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			require.Equal(t, manifest.Files[filepath.ToSlash(relative)], gitBlobHash(content), relative)
			return nil
		}))
	require.ElementsMatch(t, manifest.Evidence, actualEvidence)

	content := readFixtureFile(t, root, "azure.yaml")
	var document map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(content), &document))
	require.ElementsMatch(t, []string{"name", "metadata", "requiredVersions", "infra", "services"}, sortedMapKeys(document))
	require.Equal(t, "petclinic-rest", document["name"])
	require.Equal(t, map[string]any{"template": "petclinic-rest-azd@0.1.0"}, document["metadata"])
	versions := fixtureObject(t, document["requiredVersions"])
	require.Equal(t, ">=1.37.2", versions["azd"])
	require.Equal(t, ">=0.1.0", fixtureObject(t, versions["extensions"])["azure.springboot"])
	require.Equal(t, map[string]any{"provider": "bicep", "path": "infra", "module": "main"}, document["infra"])
	services := fixtureObject(t, document["services"])
	require.Equal(t, []string{"spring-petclinic-rest-app"}, sortedMapKeys(services))
	service := fixtureObject(t, services["spring-petclinic-rest-app"])
	require.ElementsMatch(t, []string{"project", "language", "host", "module"},
		sortedMapKeys(service))
	for key, expected := range map[string]string{
		"project": ".", "language": "java", "host": "springbootapp", "module": "application",
	} {
		require.Equal(t, expected, service[key], key)
	}
	require.NotContains(t, document, "hooks")

	var native project.ProjectConfig
	require.NoError(t, yaml.Unmarshal([]byte(content), &native))
	nativeService, ok := native.Services["spring-petclinic-rest-app"]
	require.True(t, ok)
	require.Equal(t, "application", nativeService.Module)
	require.NotContains(t, nativeService.AdditionalProperties, "module")
}

func TestSpringPetclinicRestUnchangedSourceContracts(t *testing.T) {
	root := filepath.Join(petclinicRestFixturePath(t), "upstream")
	docker := readFixtureFile(t, root, "Dockerfile")
	for _, expected := range []string{
		"maven:3.9.11-eclipse-temurin-17", "./mvnw --batch-mode", "-DskipTests package",
		"COPY --from=build", "eclipse-temurin:17-jre", "EXPOSE 9966", "-XX:MaxRAMPercentage=75.0",
	} {
		require.Contains(t, docker, expected)
	}
	main := readFixtureFile(t, root, "infra", "main.bicep")
	for _, name := range []string{
		"DATABASE_SERVER_ID", "DATABASE_HOST", "APPLICATION_PRINCIPAL_ID", "CONTAINER_APP_ENVIRONMENT_ID",
	} {
		require.Contains(t, main, "output "+name+" string")
	}
	require.NotContains(t, main, "output APPLICATION_IDENTITY_ID string")
	require.NotContains(t, main, "output APPLICATION_IDENTITY_CLIENT_ID string")
	require.NotContains(t, main, "output AZURE_CONTAINER_APP_ID ")
	mainParams := readParameterValues(t, filepath.Join(root, "infra", "main.parameters.json"))
	require.Equal(t, "${DATABASE_ADMIN_OBJECT_ID}", mainParams["databaseAdministratorObjectId"])
	require.Equal(t, "${DATABASE_ADMIN_NAME}", mainParams["databaseAdministratorName"])
	appParams := readParameterValues(t, filepath.Join(root, "infra", "application.parameters.json"))
	require.Equal(t, "${SERVICE_SPRING_PETCLINIC_REST_APP_IMAGE_NAME}", appParams["appImage"])
	require.Equal(t, "${CONTAINER_APP_ENVIRONMENT_ID}", appParams["environmentId"])
	require.Equal(t, "${DATABASE_HOST}", appParams["databaseHost"])
	require.NotContains(t, appParams, "identityId")
	require.NotContains(t, appParams, "identityClientId")
	app := readFixtureFile(t, root, "infra", "application.bicep")
	for _, expected := range []string{
		"name: 'POSTGRES_URL'", "identity: { type: 'SystemAssigned' }", "identity: 'system'",
		"targetPort: 9966", "/petclinic", "ingress: placeholder", "'azd-service-name': serviceName",
	} {
		require.Contains(t, app, expected)
	}
	require.NotContains(t, app, "AZURE_CLIENT_ID")
	require.NotContains(t, app, "'azd-env-name'")
	database := readFixtureFile(t, root, "infra", "database.bicep")
	for _, expected := range []string{"passwordAuth: 'Disabled'", "activeDirectoryAuth: 'Enabled'",
		"publicNetworkAccess: 'Enabled'", "name: 'require_secure_transport'", "value: 'on'",
		"name: 'bootstrap-local-ip'", "startIpAddress: '0.0.0.0'", "endIpAddress: '255.255.255.255'"} {
		require.Contains(t, database, expected)
	}
	properties := readFixtureFile(t, root, "src", "main", "resources", "application.properties")
	require.Contains(t, properties, "server.port=9966")
	require.Contains(t, properties, "server.servlet.context-path=/petclinic/")
	require.Contains(t, properties, "spring.sql.init.mode=always")
	for _, expected := range []string{
		"spring.profiles.active=spring-data-jpa",
		"spring.datasource.url=${POSTGRES_URL:jdbc:postgresql://localhost:5432/petclinic}",
		"spring.datasource.username=petclinic_app",
		"spring.datasource.hikari.data-source-properties[authenticationPluginClassName]=" +
			"com.azure.identity.extensions.jdbc.postgresql.AzurePostgresqlAuthenticationPlugin",
		"spring.datasource.hikari.data-source-properties[azure.managedIdentityEnabled]=true",
		"spring.datasource.hikari.data-source-properties[sslmode]=verify-full",
		"spring.datasource.hikari.data-source-properties[sslrootcert]=/etc/ssl/certs/ca-certificates.crt",
	} {
		require.Contains(t, properties, expected)
	}
	require.NotContains(t, properties, "azure.clientId")
	var pom struct {
		Parent struct {
			Version string `xml:"version"`
		} `xml:"parent"`
		Dependencies []struct {
			Group    string `xml:"groupId"`
			Artifact string `xml:"artifactId"`
			Version  string `xml:"version"`
			Scope    string `xml:"scope"`
		} `xml:"dependencies>dependency"`
	}
	require.NoError(t, xml.Unmarshal([]byte(readFixtureFile(t, root, "pom.xml")), &pom))
	require.Equal(t, "4.1.1", pom.Parent.Version)
	scopes := map[string]string{}
	for _, dependency := range pom.Dependencies {
		scopes[dependency.Group+":"+dependency.Artifact] = dependency.Scope
		if dependency.Artifact == "azure-identity-extensions" {
			require.Equal(t, "1.2.10", dependency.Version)
		}
	}
	require.Equal(t, "test", scopes["com.h2database:h2"])
	require.Equal(t, "runtime", scopes["org.postgresql:postgresql"])
	require.Equal(t, "runtime", scopes["com.azure:azure-identity-extensions"])
	controller := readFixtureFile(t, root, "src", "main", "java", "org", "springframework",
		"samples", "petclinic", "rest", "controller", "v1", "OwnerRestControllerV1.java")
	require.Contains(t, controller, `@RequestMapping("/api")`)
	require.Contains(t, controller, `.path("/api/owners/{id}")`)
	require.Contains(t, controller, "HttpStatus.CREATED")
	require.Contains(t, controller, "HttpStatus.NO_CONTENT")
}

func gitBlobHash(content []byte) string {
	// #nosec G401 -- reproduce Git's pinned blob IDs, not cryptographic trust.
	sum := sha1.Sum(append(fmt.Appendf(nil, "blob %d\x00", len(content)), content...))
	return hex.EncodeToString(sum[:])
}

// REST acceptance uses JSON owner resources, never the MVC HTML form workflow.
func petclinicRestRequest(
	t *testing.T, client *http.Client, method, endpoint, path string, body any, status int,
) []byte {
	t.Helper()
	var input io.Reader
	if body != nil {
		content, err := json.Marshal(body)
		require.NoError(t, err)
		input = bytes.NewReader(content)
	}
	request, err := http.NewRequestWithContext(t.Context(), method,
		strings.TrimSuffix(endpoint, "/")+"/api"+path, input)
	require.NoError(t, err)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, status, response.StatusCode)
	content, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	require.NoError(t, err)
	return content
}

func TestSpringPetclinicRestCRUDUsesJSONAPI(t *testing.T) {
	var calls []string
	var owner map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls = append(calls, request.Method+" "+request.URL.Path)
		require.Equal(t, "application/json", request.Header.Get("Accept"))
		require.True(t, request.URL.Path == "/petclinic/api/owners" ||
			request.URL.Path == "/petclinic/api/owners/42")
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodPost, http.MethodPut:
			require.Equal(t, "application/json", request.Header.Get("Content-Type"))
			require.NoError(t, json.NewDecoder(request.Body).Decode(&owner))
			owner["id"] = float64(42)
			if request.Method == http.MethodPost {
				writer.Header().Set("Location", "/api/owners/42")
				writer.WriteHeader(http.StatusCreated)
				require.NoError(t, json.NewEncoder(writer).Encode(owner))
			} else {
				writer.WriteHeader(http.StatusNoContent)
			}
		case http.MethodGet:
			if owner == nil {
				writer.WriteHeader(http.StatusNotFound)
			} else {
				require.NoError(t, json.NewEncoder(writer).Encode(owner))
			}
		case http.MethodDelete:
			owner = nil
			writer.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected REST method %s", request.Method)
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	endpoint := server.URL + "/petclinic"
	client := server.Client()
	input := map[string]any{"firstName": "Fixture", "lastName": "Rest",
		"address": "1 Main Street", "city": "Seattle", "telephone": "1234567890"}
	created := petclinicRestRequest(t, client, http.MethodPost, endpoint, "/owners", input, http.StatusCreated)
	var actual map[string]any
	require.NoError(t, json.Unmarshal(created, &actual))
	require.Equal(t, float64(42), actual["id"])
	require.Equal(t, "Rest", actual["lastName"])
	content := petclinicRestRequest(t, client, http.MethodGet, endpoint, "/owners/42", nil, http.StatusOK)
	require.JSONEq(t, string(created), string(content))
	input["city"] = "Redmond"
	petclinicRestRequest(t, client, http.MethodPut, endpoint, "/owners/42", input, http.StatusNoContent)
	content = petclinicRestRequest(t, client, http.MethodGet, endpoint, "/owners/42", nil, http.StatusOK)
	require.NoError(t, json.Unmarshal(content, &actual))
	require.Equal(t, "Redmond", actual["city"])
	petclinicRestRequest(t, client, http.MethodDelete, endpoint, "/owners/42", nil, http.StatusNoContent)
	petclinicRestRequest(t, client, http.MethodGet, endpoint, "/owners/42", nil, http.StatusNotFound)
	require.Equal(t, []string{
		"POST /petclinic/api/owners", "GET /petclinic/api/owners/42", "PUT /petclinic/api/owners/42",
		"GET /petclinic/api/owners/42", "DELETE /petclinic/api/owners/42", "GET /petclinic/api/owners/42",
	}, calls)
}
