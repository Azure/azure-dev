// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/test/azdcli"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/require"
)

func Test_CLI_SpringPetclinic_Live(t *testing.T) {
	if os.Getenv("AZD_TEST_SPRING_PETCLINIC_LIVE") != "1" {
		t.Skip("set AZD_TEST_SPRING_PETCLINIC_LIVE=1 after explicit Azure-cost authorization")
	}
	configDir := os.Getenv("AZD_TEST_SPRING_PETCLINIC_CONFIG_DIR")
	require.NotEmpty(t, configDir, "provide a disposable, already authenticated AZD config directory")
	require.NotEmpty(t, cfg.SubscriptionID, "AZD_TEST_AZURE_SUBSCRIPTION_ID is required")
	require.NotEmpty(t, cfg.Location, "AZD_TEST_AZURE_LOCATION is required")

	installCtx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	installSpringExtensionFixture(t, installCtx, configDir)

	for _, mode := range []string{"h2", "postgresql"} {
		t.Run(mode, func(t *testing.T) {
			runSpringPetclinicLiveMode(t, configDir, mode)
		})
	}
}

func runSpringPetclinicLiveMode(t *testing.T, configDir, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Minute)
	defer cancel()

	projectDir := tempDirWithDiagnostics(t)
	require.NoError(t, copySample(projectDir, "spring-petclinic/overlay"))
	envName := "petclinic-" + mode + "-" + randomEnvName()
	cli := azdcli.NewCLI(t)
	cli.WorkingDirectory = projectDir
	cli.Env = append(environmentWithout("SPRING_DATABASE_TYPE", "SPRING_DATABASE_PASSWORD"), cli.Env...)
	cli.Env = append(
		cli.Env,
		"AZD_CONFIG_DIR="+configDir,
		"AZD_ALLOW_NON_EMPTY_FOLDER=1",
		"AZURE_DEV_COLLECT_TELEMETRY=no",
		"AZD_FORCE_TTY=false",
		"NO_COLOR=1",
		"AZURE_SUBSCRIPTION_ID="+cfg.SubscriptionID,
		"AZURE_LOCATION="+cfg.Location,
	)

	_, err := cli.RunCommand(
		ctx,
		"init",
		"-t", petclinicRepository,
		".",
		"-e", envName,
		"--no-prompt",
	)
	require.NoError(t, err)
	_, err = cli.RunCommand(ctx, "env", "set", "SPRING_DATABASE_TYPE", mode)
	require.NoError(t, err)
	ownedResources := map[string]string{}
	expectedPassword := ""
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cleanupCancel()
		if _, err := cli.RunCommand(cleanupCtx, "down", "--force", "--purge"); err != nil {
			t.Errorf("Spring Petclinic live cleanup failed: %v", err)
		}
		verifySpringOwnedResourcesRemoved(t, cleanupCtx, cli, ownedResources)
		if expectedPassword != "" {
			verifySpringLocalPasswordRetained(t, cleanupCtx, cli, expectedPassword)
		}
	})

	up, err := cli.RunCommand(ctx, "up", "--no-prompt")
	require.NoError(t, err, "azd up failed")
	values := springLiveEnvironment(t, ctx, cli)
	password := values["SPRING_DATABASE_PASSWORD"]
	if password != "" {
		assertSpringSecretNotDisclosed(t, up, password)
	}

	endpoint := strings.TrimSuffix(values["SERVICE_PETCLINIC_URI"], "/")
	require.NotEmpty(t, endpoint)
	if mode == "h2" {
		require.Empty(t, password)
		require.Empty(t, values["AZURE_POSTGRES_SERVER_ID"])
	} else {
		require.NotEmpty(t, password)
		require.NotEmpty(t, values["AZURE_POSTGRES_SERVER_ID"])
		expectedPassword = password
		verifySpringLiveFirewall(t, ctx, cli, values)
	}
	ownedResources[values["AZURE_CONTAINER_APP_ID"]] = "2024-03-01"
	if values["AZURE_POSTGRES_SERVER_ID"] != "" {
		ownedResources[values["AZURE_POSTGRES_SERVER_ID"]] = "2023-12-01-preview"
	}

	ownerID, marker := createPetclinicMvcRecord(t, endpoint)
	requirePetclinicRecord(t, endpoint, ownerID, marker, true)
	revisionBefore := springLatestRevision(t, ctx, cli, values["AZURE_CONTAINER_APP_ID"])

	deploy, err := cli.RunCommand(ctx, "deploy", "--no-prompt")
	require.NoError(t, err, "azd deploy failed")
	if password != "" {
		assertSpringSecretNotDisclosed(t, deploy, password)
	}
	revisionAfter := waitForSpringRevisionChange(t, ctx, cli, values["AZURE_CONTAINER_APP_ID"], revisionBefore)
	require.NotEqual(t, revisionBefore, revisionAfter, "deploy must replace the running revision")
	waitForSpringReadiness(t, endpoint)
	requirePetclinicRecord(t, endpoint, ownerID, marker, mode == "postgresql")

	if mode != "postgresql" {
		return
	}

	reprovision, err := cli.RunCommand(ctx, "provision", "--no-prompt")
	require.NoError(t, err)
	assertSpringSecretNotDisclosed(t, reprovision, password)
	waitForSpringReadiness(t, endpoint)
	requirePetclinicRecord(t, endpoint, ownerID, marker, true)

	repeated, err := cli.RunCommand(ctx, "up", "--no-prompt")
	require.NoError(t, err, "repeated azd up failed")
	assertSpringSecretNotDisclosed(t, repeated, password)
	waitForSpringReadiness(t, endpoint)
	requirePetclinicRecord(t, endpoint, ownerID, marker, true)
	verifySpringLiveFirewall(t, ctx, cli, springLiveEnvironment(t, ctx, cli))
	verifySpringFreshRunnerPasswordRestoration(
		t,
		ctx,
		cli,
		projectDir,
		envName,
		values,
		endpoint,
		ownerID,
		marker,
	)
}

func springLiveEnvironment(t *testing.T, ctx context.Context, cli *azdcli.CLI) map[string]string {
	t.Helper()
	result, err := cli.RunCommand(ctx, "env", "get-values", "--output", "json")
	require.NoError(t, err)
	values := map[string]string{}
	require.NoError(t, json.Unmarshal([]byte(result.Stdout), &values))
	return values
}

func springAzdToken(t *testing.T, ctx context.Context, cli *azdcli.CLI) string {
	t.Helper()
	token, err := springAzdTokenValue(ctx, cli)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	return token
}

func springAzdTokenValue(ctx context.Context, cli *azdcli.CLI) (string, error) {
	result, err := cli.RunCommand(
		ctx,
		"auth", "token",
		"--scope", "https://management.azure.com/.default",
		"--output", "json",
		"--no-prompt",
	)
	if err != nil {
		return "", err
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
		return "", err
	}
	if response.Token == "" {
		return "", fmt.Errorf("azd returned an empty ARM token")
	}
	return response.Token, nil
}

func springArmGet(
	t *testing.T,
	ctx context.Context,
	cli *azdcli.CLI,
	resourceID,
	apiVersion string,
	result any,
) {
	t.Helper()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://management.azure.com"+resourceID+"?api-version="+url.QueryEscape(apiVersion),
		nil,
	)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+springAzdToken(t, ctx, cli))
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode, "ARM response: %s", body)
	require.NoError(t, json.Unmarshal(body, result))
}

func springLatestRevision(t *testing.T, ctx context.Context, cli *azdcli.CLI, appID string) string {
	t.Helper()
	var app struct {
		Properties struct {
			LatestRevisionName string `json:"latestRevisionName"`
		} `json:"properties"`
	}
	springArmGet(t, ctx, cli, appID, "2024-03-01", &app)
	require.NotEmpty(t, app.Properties.LatestRevisionName)
	return app.Properties.LatestRevisionName
}

func waitForSpringRevisionChange(
	t *testing.T,
	ctx context.Context,
	cli *azdcli.CLI,
	appID,
	previous string,
) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		current := springLatestRevision(t, ctx, cli, appID)
		if current != previous {
			return current
		}
		time.Sleep(10 * time.Second)
	}
	t.Fatalf("Container App revision did not change from %q", previous)
	return ""
}

func verifySpringLiveFirewall(
	t *testing.T,
	ctx context.Context,
	cli *azdcli.CLI,
	values map[string]string,
) {
	t.Helper()
	var app struct {
		Properties struct {
			OutboundIPAddresses []string `json:"outboundIpAddresses"`
		} `json:"properties"`
	}
	springArmGet(t, ctx, cli, values["AZURE_CONTAINER_APP_ID"], "2024-03-01", &app)
	require.NotEmpty(t, app.Properties.OutboundIPAddresses)

	var rules struct {
		Value []struct {
			Name       string `json:"name"`
			Properties struct {
				Start string `json:"startIpAddress"`
				End   string `json:"endIpAddress"`
			} `json:"properties"`
		} `json:"value"`
	}
	springArmGet(
		t,
		ctx,
		cli,
		values["AZURE_POSTGRES_SERVER_ID"]+"/firewallRules",
		"2021-06-01",
		&rules,
	)
	for _, ip := range app.Properties.OutboundIPAddresses {
		found := false
		for _, rule := range rules.Value {
			if strings.HasPrefix(rule.Name, "azd-springboot-") &&
				rule.Properties.Start == ip && rule.Properties.End == ip {
				found = true
				break
			}
		}
		require.Truef(t, found, "missing exact owned PostgreSQL firewall rule for %s", ip)
	}
}

func createPetclinicMvcRecord(t *testing.T, endpoint string) (string, string) {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar, Timeout: 30 * time.Second}
	stamp := fmt.Sprint(time.Now().UnixNano())

	response := postPetclinicForm(t, client, endpoint+"/owners/new", url.Values{
		"firstName": {"Azd"},
		"lastName":  {"Owner" + stamp},
		"address":   {"1 Azure Way"},
		"city":      {"Redmond"},
		"telephone": {"4255550100"},
	})
	ownerMatch := regexp.MustCompile(`/owners/(\d+)$`).FindStringSubmatch(response.Request.URL.Path)
	_ = response.Body.Close()
	require.Len(t, ownerMatch, 2)
	ownerID := ownerMatch[1]

	response = postPetclinicForm(t, client, endpoint+"/owners/"+ownerID+"/pets/new", url.Values{
		"name":      {"Pet" + stamp},
		"birthDate": {"2020-01-02"},
		"type":      {"cat"},
	})
	body := readPetclinicResponse(t, response)
	petMatch := regexp.MustCompile(`pets/(\d+)/edit`).FindStringSubmatch(body)
	require.Len(t, petMatch, 2)

	marker := "Visit-" + stamp
	response = postPetclinicForm(
		t,
		client,
		endpoint+"/owners/"+ownerID+"/pets/"+petMatch[1]+"/visits/new",
		url.Values{
			"date":        {time.Now().Add(48 * time.Hour).Format("2006-01-02")},
			"description": {marker},
		},
	)
	_ = readPetclinicResponse(t, response)
	return ownerID, marker
}

func postPetclinicForm(t *testing.T, client *http.Client, endpoint string, values url.Values) *http.Response {
	t.Helper()
	response, err := client.PostForm(endpoint, values)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	return response
}

func readPetclinicResponse(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	require.NoError(t, err)
	return string(body)
}

func requirePetclinicRecord(t *testing.T, endpoint, ownerID, marker string, present bool) {
	t.Helper()
	response, err := http.Get(endpoint + "/owners/" + ownerID)
	if !present {
		if err == nil {
			defer response.Body.Close()
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
			require.NoError(t, readErr)
			require.NotContains(t, string(body), marker)
		}
		return
	}
	require.NoError(t, err)
	body := readPetclinicResponse(t, response)
	require.Contains(t, body, marker)
}

func waitForSpringReadiness(t *testing.T, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint + "/actuator/health/readiness")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Second)
	}
	t.Fatalf("Petclinic did not become ready at %s", endpoint)
}

func verifySpringFreshRunnerPasswordRestoration(
	t *testing.T,
	ctx context.Context,
	cli *azdcli.CLI,
	projectDir,
	envName string,
	before map[string]string,
	endpoint,
	ownerID,
	marker string,
) {
	t.Helper()
	originalPassword := before["SPRING_DATABASE_PASSWORD"]
	originalServerID := before["AZURE_POSTGRES_SERVER_ID"]
	require.NotEmpty(t, originalPassword)
	require.NotEmpty(t, originalServerID)

	envPath := filepath.Join(projectDir, ".azure", envName, ".env")
	persisted, err := godotenv.Read(envPath)
	require.NoError(t, err)
	for _, key := range []string{
		"SPRING_DATABASE_PASSWORD",
		"AZURE_POSTGRES_SERVER_ID",
		"AZURE_POSTGRES_SERVER_NAME",
		"AZURE_POSTGRES_HOST",
		"AZURE_POSTGRES_DATABASE",
		"AZURE_POSTGRES_USER",
	} {
		delete(persisted, key)
	}
	require.NoError(t, godotenv.Write(persisted, envPath))

	missing, err := cli.RunCommand(ctx, "provision", "--no-prompt")
	require.Error(t, err, "existing PostgreSQL must not silently receive a generated password")
	require.NotNil(t, missing, "provision command did not start: %v", err)
	assertSpringSecretNotDisclosed(t, missing, originalPassword)

	emptyCLI := springCLIWithPassword(cli, "")
	empty, err := emptyCLI.RunCommand(ctx, "provision", "--no-prompt")
	require.Error(t, err, "an explicitly empty original password must be rejected")
	require.NotNil(t, empty, "provision command did not start: %v", err)
	assertSpringSecretNotDisclosed(t, empty, originalPassword)

	restoreCLI := springCLIWithPassword(cli, originalPassword)
	restored, err := restoreCLI.RunCommand(ctx, "provision", "--no-prompt")
	require.NoError(t, err, "restoring the original password should permit reprovision")
	assertSpringSecretNotDisclosed(t, restored, originalPassword)

	after := springLiveEnvironment(t, ctx, cli)
	if after["SPRING_DATABASE_PASSWORD"] != originalPassword {
		t.Error("the restored PostgreSQL password was not retained unchanged")
	}
	if after["AZURE_POSTGRES_SERVER_ID"] != originalServerID {
		t.Error("reprovision replaced the existing PostgreSQL server")
	}

	conflictCLI := springCLIWithPassword(cli, "Different-Azd9!")
	conflict, err := conflictCLI.RunCommand(ctx, "provision", "--no-prompt")
	require.Error(t, err, "a conflicting supplied password must be rejected")
	require.NotNil(t, conflict, "provision command did not start: %v", err)
	assertSpringSecretNotDisclosed(t, conflict, originalPassword)

	waitForSpringReadiness(t, endpoint)
	requirePetclinicRecord(t, endpoint, ownerID, marker, true)
}

func springCLIWithPassword(cli *azdcli.CLI, password string) *azdcli.CLI {
	cloned := *cli
	cloned.Env = slices.DeleteFunc(slices.Clone(cli.Env), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return strings.EqualFold(name, "SPRING_DATABASE_PASSWORD")
	})
	cloned.Env = append(cloned.Env, "SPRING_DATABASE_PASSWORD="+password)
	return &cloned
}

func assertSpringSecretNotDisclosed(t *testing.T, result *azdcli.CliResult, secret string) {
	t.Helper()
	if secret != "" && (strings.Contains(result.Stdout, secret) || strings.Contains(result.Stderr, secret)) {
		t.Error("azd command output disclosed the PostgreSQL password")
	}
}

func verifySpringOwnedResourcesRemoved(
	t *testing.T,
	ctx context.Context,
	cli *azdcli.CLI,
	resources map[string]string,
) {
	t.Helper()
	token, err := springAzdTokenValue(ctx, cli)
	if err != nil {
		t.Errorf("obtaining ARM token for cleanup verification: %v", err)
		return
	}
	for resourceID, apiVersion := range resources {
		if resourceID == "" {
			continue
		}
		deadline := time.Now().Add(10 * time.Minute)
		for {
			request, err := http.NewRequestWithContext(
				ctx,
				http.MethodGet,
				"https://management.azure.com"+resourceID+"?api-version="+url.QueryEscape(apiVersion),
				nil,
			)
			if err != nil {
				t.Errorf("creating cleanup verification request: %v", err)
				break
			}
			request.Header.Set("Authorization", "Bearer "+token)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Errorf("checking owned resource cleanup: %v", err)
				break
			}
			_ = response.Body.Close()
			if response.StatusCode == http.StatusNotFound {
				break
			}
			if response.StatusCode != http.StatusOK {
				t.Errorf("owned resource cleanup returned HTTP %d", response.StatusCode)
				break
			}
			if time.Now().After(deadline) {
				t.Error("owned resource still exists after azd down")
				break
			}
			time.Sleep(10 * time.Second)
		}
	}
}

func verifySpringLocalPasswordRetained(
	t *testing.T,
	ctx context.Context,
	cli *azdcli.CLI,
	expected string,
) {
	t.Helper()
	result, err := cli.RunCommand(ctx, "env", "get-values", "--output", "json")
	if err != nil {
		t.Errorf("reading local environment after cleanup: %v", err)
		return
	}
	values := map[string]string{}
	if err := json.Unmarshal([]byte(result.Stdout), &values); err != nil {
		t.Errorf("decoding local environment after cleanup: %v", err)
		return
	}
	if values["SPRING_DATABASE_PASSWORD"] != expected {
		t.Error("azd down removed or changed the retained local PostgreSQL password")
	}
}
