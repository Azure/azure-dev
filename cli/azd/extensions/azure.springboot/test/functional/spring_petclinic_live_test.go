// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cli_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	osexec "os/exec"
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
	require.NotEmpty(t, os.Getenv("AZD_TEST_AZURE_SUBSCRIPTION_ID"), "AZD_TEST_AZURE_SUBSCRIPTION_ID is required")
	require.NotEmpty(t, os.Getenv("AZD_TEST_AZURE_LOCATION"), "AZD_TEST_AZURE_LOCATION is required")

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

	envName := "petclinic-" + mode + "-" + strings.ToLower(rand.Text()[:8])
	cli := initPetclinicTemplate(
		t, ctx, configDir, envName, mode,
		"AZURE_SUBSCRIPTION_ID="+os.Getenv("AZD_TEST_AZURE_SUBSCRIPTION_ID"),
		"AZURE_LOCATION="+os.Getenv("AZD_TEST_AZURE_LOCATION"),
	)
	projectDir := cli.WorkingDirectory
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
	stdout, err := runSpringCommandWithoutLogging(ctx, cli, "env", "get-values", "--output", "json")
	require.NoError(t, err)
	values := map[string]string{}
	require.NoError(t, json.Unmarshal([]byte(stdout), &values))
	return values
}

func runSpringCommandWithoutLogging(ctx context.Context, cli *azdcli.CLI, args ...string) (string, error) {
	command := osexec.CommandContext(ctx, cli.AzdPath, args...) // #nosec G204 -- Test-owned azd arguments.
	command.Dir = cli.WorkingDirectory
	command.Env = cli.Env
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("running azd command: %w", err)
	}
	return stdout.String(), nil
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

	response := postPetclinicForm(
		t,
		client,
		endpoint+"/owners/new",
		url.Values{
			"firstName": {"Azd"},
			"lastName":  {"Owner" + stamp},
			"address":   {"1 Azure Way"},
			"city":      {"Redmond"},
			"telephone": {"4255550100"},
		},
		regexp.MustCompile(`/owners/\d+$`),
	)
	ownerMatch := regexp.MustCompile(`/owners/(\d+)$`).FindStringSubmatch(petclinicRoutePath(response.Request.URL))
	_ = response.Body.Close()
	require.Len(t, ownerMatch, 2)
	ownerID := ownerMatch[1]

	response = postPetclinicForm(
		t,
		client,
		endpoint+"/owners/"+ownerID+"/pets/new",
		url.Values{
			"name":      {"Pet" + stamp},
			"birthDate": {"2020-01-02"},
			"type":      {"cat"},
		},
		regexp.MustCompile(`/owners/`+regexp.QuoteMeta(ownerID)+`$`),
	)
	body := readPetclinicResponse(t, response)
	petID, err := petclinicPetEditID(response, body, "Pet"+stamp)
	require.NoError(t, err)

	marker := "Visit-" + stamp
	response = postPetclinicForm(
		t,
		client,
		endpoint+"/owners/"+ownerID+"/pets/"+petID+"/visits/new",
		url.Values{
			"date":        {time.Now().Add(48 * time.Hour).Format("2006-01-02")},
			"description": {marker},
		},
		regexp.MustCompile(`/owners/`+regexp.QuoteMeta(ownerID)+`$`),
	)
	_ = readPetclinicResponse(t, response)
	return ownerID, marker
}

func postPetclinicForm(
	t *testing.T,
	client *http.Client,
	endpoint string,
	values url.Values,
	expectedPath *regexp.Regexp,
) *http.Response {
	t.Helper()
	response, err := submitPetclinicForm(client, endpoint, values, expectedPath)
	require.NoError(t, err)
	return response
}

func submitPetclinicForm(
	client *http.Client,
	endpoint string,
	values url.Values,
	expectedPath *regexp.Regexp,
) (*http.Response, error) {
	response, err := client.PostForm(endpoint, values)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, fmt.Errorf(
			"Petclinic form submission returned %s after redirects %s",
			response.Status,
			petclinicRedirectContext(response.Request),
		)
	}
	if !petclinicHasSuccessRedirect(response.Request) {
		diagnostics, readErr := petclinicValidationDiagnostics(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("reading unsuccessful Petclinic form response: %w", readErr)
		}
		return nil, fmt.Errorf(
			"Petclinic form submission returned final status=%s path=%q redirects=none validation=%s",
			response.Status,
			petclinicRoutePath(response.Request.URL),
			diagnostics,
		)
	}
	if expectedPath == nil || !expectedPath.MatchString(petclinicRoutePath(response.Request.URL)) {
		diagnostics, readErr := petclinicValidationDiagnostics(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("reading unsuccessful Petclinic form response: %w", readErr)
		}
		return nil, fmt.Errorf(
			"Petclinic form submission did not reach expected owner result; "+
				"final status=%s path=%q redirects=%s validation=%s",
			response.Status,
			petclinicRoutePath(response.Request.URL),
			petclinicRedirectContext(response.Request),
			diagnostics,
		)
	}
	return response, nil
}

func petclinicHasSuccessRedirect(request *http.Request) bool {
	for current := request; current != nil && current.Response != nil; current = current.Response.Request {
		if current.Response.StatusCode == http.StatusFound ||
			current.Response.StatusCode == http.StatusSeeOther {
			return true
		}
	}
	return false
}

func petclinicRedirectContext(request *http.Request) string {
	steps := make([]string, 0, 2)
	for current := request; current != nil && current.Response != nil; current = current.Response.Request {
		location, err := current.Response.Location()
		if err != nil {
			steps = append(steps, fmt.Sprintf("%d:<invalid-location>", current.Response.StatusCode))
			continue
		}
		steps = append(steps, fmt.Sprintf(
			"%d:%s",
			current.Response.StatusCode,
			petclinicRoutePath(location),
		))
	}
	slices.Reverse(steps)
	if len(steps) == 0 {
		return "none"
	}
	return strings.Join(steps, " -> ")
}

func petclinicRoutePath(value *url.URL) string {
	if value == nil {
		return "<missing-path>"
	}
	segments := strings.Split(value.Path, "/")
	for index, segment := range segments {
		segments[index], _, _ = strings.Cut(segment, ";")
	}
	route := strings.Join(segments, "/")
	if route == "" {
		return "/"
	}
	return route
}

var petclinicValidationTextPattern = regexp.MustCompile(
	`(?is)<(?:span|div)[^>]*class=["'][^"']*(?:help-inline|alert-danger)[^"']*["'][^>]*>(.*?)</(?:span|div)>`,
)
var petclinicHTMLTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)
var petclinicHrefPattern = regexp.MustCompile(`(?is)href=["']([^"']*pets[^"']*)["']`)

func petclinicValidationDiagnostics(body io.Reader) (string, error) {
	content, err := io.ReadAll(io.LimitReader(body, 256<<10))
	if err != nil {
		return "", err
	}
	return petclinicValidationText(content), nil
}

func petclinicValidationText(content []byte) string {
	matches := petclinicValidationTextPattern.FindAllSubmatch(content, 5)
	messages := make([]string, 0, len(matches))
	for _, match := range matches {
		text := petclinicHTMLTagPattern.ReplaceAllString(string(match[1]), " ")
		text = strings.Join(strings.Fields(html.UnescapeString(text)), " ")
		if text != "" && !slices.Contains(messages, text) {
			messages = append(messages, text)
		}
	}
	if len(messages) == 0 {
		return "no validation message found"
	}
	return strings.Join(messages, "; ")
}

func petclinicPetEditID(response *http.Response, body, requestedPetName string) (string, error) {
	hrefMatches := petclinicHrefPattern.FindAllStringSubmatch(body, -1)
	const maxDiagnosticHrefs = 5
	hrefs := make([]string, 0, min(len(hrefMatches), maxDiagnosticHrefs))
	for index, match := range hrefMatches {
		href, err := url.Parse(html.UnescapeString(match[1]))
		if err != nil {
			if index < maxDiagnosticHrefs {
				hrefs = append(hrefs, "<invalid-href>")
			}
			continue
		}
		route := petclinicRoutePath(href)
		if petMatch := regexp.MustCompile(`/pets/(\d+)/edit$`).FindStringSubmatch(route); len(petMatch) == 2 {
			return petMatch[1], nil
		}
		if index < maxDiagnosticHrefs {
			hrefs = append(hrefs, route)
		}
	}

	return "", fmt.Errorf(
		"Petclinic owner result has no pet edit link; final status=%s path=%q redirects=%s "+
			"requestedPetNamePresent=%t validation=%s petHrefCount=%d petHrefs=%v",
		response.Status,
		petclinicRoutePath(response.Request.URL),
		petclinicRedirectContext(response.Request),
		strings.Contains(body, requestedPetName),
		petclinicValidationText([]byte(body)),
		len(hrefMatches),
		hrefs,
	)
}

func readPetclinicResponse(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	require.NoError(t, err)
	return string(body)
}

func TestSubmitPetclinicFormAcceptsCreationRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(writer, request.Body, 64*1024)
		switch request.URL.Path {
		case "/owners/new":
			if request.Method != http.MethodPost {
				http.Error(writer, "expected POST", http.StatusMethodNotAllowed)
				return
			}
			http.SetCookie(writer, &http.Cookie{Name: "petclinic", Value: "created", Path: "/"})
			http.Redirect(writer, request, "/owners/42", http.StatusSeeOther)
		case "/owners/42":
			if request.Method != http.MethodGet {
				http.Error(writer, "expected GET", http.StatusMethodNotAllowed)
				return
			}
			cookie, err := request.Cookie("petclinic")
			if err != nil || cookie.Value != "created" {
				http.Error(writer, "cookie missing", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(writer, "<html><body>Owner 42</body></html>")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := server.Client()
	client.Jar = jar
	response, err := submitPetclinicForm(
		client,
		server.URL+"/owners/new",
		url.Values{"firstName": {"Azd"}},
		regexp.MustCompile(`/owners/\d+$`),
	)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, "/owners/42", response.Request.URL.Path)
	require.Equal(t, "303:/owners/42", petclinicRedirectContext(response.Request))
}

func TestCreatePetclinicMvcRecordSupportsServletRoutes(t *testing.T) {
	for _, test := range []struct {
		name        string
		rewriteURLs bool
	}{
		{name: "regular paths"},
		{name: "matrix parameter paths", rewriteURLs: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			const ownerID = "11"
			const petID = "22"
			var petName string
			var visitMarker string
			redirectOwner := func(writer http.ResponseWriter, request *http.Request) {
				location := "/owners/" + ownerID
				if test.rewriteURLs {
					location += ";jsessionid=redirect-session"
				}
				http.Redirect(writer, request, location, http.StatusFound)
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				request.Body = http.MaxBytesReader(writer, request.Body, 64*1024)
				route := petclinicRoutePath(request.URL)
				switch {
				case request.Method == http.MethodPost && route == "/owners/new":
					if err := request.ParseForm(); err != nil {
						http.Error(writer, "invalid owner form", http.StatusBadRequest)
						return
					}
					redirectOwner(writer, request)
				case request.Method == http.MethodPost && route == "/owners/"+ownerID+"/pets/new":
					if err := request.ParseForm(); err != nil {
						http.Error(writer, "invalid pet form", http.StatusBadRequest)
						return
					}
					petName = request.Form.Get("name")
					redirectOwner(writer, request)
				case request.Method == http.MethodPost &&
					route == "/owners/"+ownerID+"/pets/"+petID+"/visits/new":
					if err := request.ParseForm(); err != nil {
						http.Error(writer, "invalid visit form", http.StatusBadRequest)
						return
					}
					visitMarker = request.Form.Get("description")
					redirectOwner(writer, request)
				case request.Method == http.MethodGet && route == "/owners/"+ownerID:
					petEditPath := "/owners/" + ownerID + "/pets/" + petID + "/edit"
					if test.rewriteURLs {
						petEditPath = "/owners/" + ownerID +
							";owner=matrix/pets/" + petID + ";jsessionid=href-session/edit;view=details"
					}
					_, _ = fmt.Fprintf(
						writer,
						`<html><body>%s <a href=%q>Edit Pet</a> %s</body></html>`,
						html.EscapeString(petName),
						petEditPath,
						html.EscapeString(visitMarker),
					)
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			actualOwnerID, marker := createPetclinicMvcRecord(t, server.URL)
			require.Equal(t, ownerID, actualOwnerID)
			require.NotEmpty(t, petName)
			require.Equal(t, marker, visitMarker)
			requirePetclinicRecord(t, server.URL, actualOwnerID, marker, true)
		})
	}
}

func TestSubmitPetclinicFormRejectsValidationResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/owners/new" {
			http.Error(writer, "unexpected path", http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(
			writer,
			`<html><body><span class="help-inline">must not be blank</span>`+
				`<div class="alert alert-danger">Telephone &amp; city are invalid</div>`+
				`<input name="session" value="sensitive"></body></html>`,
		)
	}))
	defer server.Close()

	_, err := submitPetclinicForm(
		server.Client(),
		server.URL+"/owners/new",
		url.Values{},
		regexp.MustCompile(`/owners/\d+$`),
	)
	require.ErrorContains(t, err, "final status=200 OK")
	require.ErrorContains(t, err, "redirects=none")
	require.ErrorContains(t, err, "must not be blank")
	require.ErrorContains(t, err, "Telephone & city are invalid")
	require.NotContains(t, err.Error(), "sensitive")
	require.NotContains(t, err.Error(), "<html>")
}

func TestPetclinicDiagnosticsRedactCredentialBearingPaths(t *testing.T) {
	finalURL, err := url.Parse(
		"https://final-user:final-password@example.test/owners/42;jsessionid=final-session?token=final-token#final-fragment",
	)
	require.NoError(t, err)
	redirectURL, err := url.Parse(
		"https://redirect-user:redirect-password@example.test/owners/42" +
			";jsessionid=redirect-session?sig=redirect-token#redirect-fragment",
	)
	require.NoError(t, err)
	originalURL, err := url.Parse(
		"https://request-user:request-password@example.test/owners/new" +
			";jsessionid=request-session?key=request-token#request-fragment",
	)
	require.NoError(t, err)
	finalRequest := &http.Request{
		URL: finalURL,
		Response: &http.Response{
			StatusCode: http.StatusSeeOther,
			Header:     http.Header{"Location": []string{redirectURL.String()}},
			Request:    &http.Request{URL: originalURL},
		},
	}

	diagnostics := petclinicRoutePath(finalRequest.URL) + " " + petclinicRedirectContext(finalRequest)
	require.Equal(t, "/owners/42 303:/owners/42", diagnostics)
	for _, sensitive := range []string{
		"final-user", "final-password", "final-session", "final-token", "final-fragment",
		"redirect-user", "redirect-password", "redirect-session", "redirect-token", "redirect-fragment",
		"request-user", "request-password", "request-session", "request-token", "request-fragment",
	} {
		require.NotContains(t, diagnostics, sensitive)
	}
}

func TestPetclinicRedirectContextReportsInvalidLocation(t *testing.T) {
	request := &http.Request{
		URL: &url.URL{Path: "/owners/new"},
		Response: &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://[::1"}},
			Request:    &http.Request{URL: &url.URL{Path: "/owners/new"}},
		},
	}

	require.Equal(t, "302:<invalid-location>", petclinicRedirectContext(request))
}

func TestPetclinicPetEditIDReturnsSanitizedDiagnostics(t *testing.T) {
	finalURL, err := url.Parse("/owners/42;jsessionid=final-session?token=final-token#final-fragment")
	require.NoError(t, err)
	response := &http.Response{
		Status: http.StatusText(http.StatusOK),
		Request: &http.Request{
			URL: finalURL,
			Response: &http.Response{
				StatusCode: http.StatusFound,
				Header: http.Header{
					"Location": []string{
						"https://redirect-user:redirect-password@example.test/owners/42;" +
							"jsessionid=redirect-session?sig=redirect-token#redirect-fragment",
					},
				},
				Request: &http.Request{URL: &url.URL{Path: "/owners/42"}},
			},
		},
	}
	body := `<html><body>Pet123` +
		`<span class="help-inline">binding failed</span>` +
		`<a href="https://pet-user:pet-password@example.test/owners/42/pets;jsessionid=pet-session?sig=pet-token">pet</a>` +
		`<a href="/owners/42/pets/%zz">invalid pet link</a>` +
		`<input value="sensitive"></body></html>`

	_, err = petclinicPetEditID(response, body, "Pet123")
	require.ErrorContains(t, err, "requestedPetNamePresent=true")
	require.ErrorContains(t, err, "validation=binding failed")
	require.ErrorContains(t, err, "petHrefCount=2")
	require.ErrorContains(t, err, "petHrefs=[/owners/42/pets <invalid-href>]")
	for _, sensitive := range []string{
		"final-session", "final-token", "final-fragment",
		"redirect-user", "redirect-password", "redirect-session", "redirect-token", "redirect-fragment",
		"pet-user", "pet-password", "pet-session", "pet-token", "sensitive", "<html>",
	} {
		require.NotContains(t, err.Error(), sensitive)
	}
}

func requirePetclinicRecord(t *testing.T, endpoint, ownerID, marker string, present bool) {
	t.Helper()
	response, err := http.Get(endpoint + "/owners/" + ownerID)
	if !present {
		if err == nil {
			defer response.Body.Close()
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
			require.NoError(t, readErr)
			require.False(
				t,
				strings.Contains(string(body), marker),
				"Petclinic owner record unexpectedly retained marker; status=%s path=%q",
				response.Status,
				petclinicRoutePath(response.Request.URL),
			)
		}
		return
	}
	require.NoError(t, err)
	body := readPetclinicResponse(t, response)
	require.True(
		t,
		strings.Contains(body, marker),
		"Petclinic owner record is missing marker; status=%s path=%q",
		response.Status,
		petclinicRoutePath(response.Request.URL),
	)
}

func waitForSpringReadiness(t *testing.T, endpoint string) {
	t.Helper()
	client := &http.Client{Timeout: 30 * time.Second}
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		response, err := client.Get(endpoint + "/actuator/health/readiness")
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
