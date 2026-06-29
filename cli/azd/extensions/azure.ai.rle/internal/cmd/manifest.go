// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore openenv

package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"gopkg.in/yaml.v3"
)

type rleManifest struct {
	Name              string                  `yaml:"name"`
	Description       string                  `yaml:"description"`
	Kind              string                  `yaml:"kind"`
	LocalImage        string                  `yaml:"local_image"`
	Image             string                  `yaml:"image"`
	RegistrationImage string                  `yaml:"registration_image"`
	Template          rleManifestTemplate     `yaml:"template"`
	Local             rleManifestLocal        `yaml:"local"`
	Registration      rleManifestRegistration `yaml:"registration"`
	Environment       rleManifestEnvironment  `yaml:"environment"`
}

type rleManifestTemplate struct {
	Name              string                  `yaml:"name"`
	Kind              string                  `yaml:"kind"`
	Local             rleManifestLocal        `yaml:"local"`
	Environment       rleManifestEnvironment  `yaml:"environment"`
	Registration      rleManifestRegistration `yaml:"registration"`
	Image             string                  `yaml:"image"`
	LocalImage        string                  `yaml:"local_image"`
	RegistrationImage string                  `yaml:"registration_image"`
}

type rleManifestLocal struct {
	Image string `yaml:"image"`
}

type rleManifestRegistration struct {
	Image string `yaml:"image"`
}

type rleManifestEnvironment struct {
	Image string `yaml:"image"`
}

func loadRleManifest(path string) (rleManifest, error) {
	data, err := readManifestContent(context.Background(), path)
	if err != nil {
		return rleManifest{}, err
	}
	return parseRleManifest(data)
}

func parseRleManifest(data []byte) (rleManifest, error) {
	expanded, err := expandManifestEnv(string(data))
	if err != nil {
		return rleManifest{}, err
	}

	var manifest rleManifest
	if err := yaml.Unmarshal([]byte(expanded), &manifest); err != nil {
		return rleManifest{}, err
	}
	if err := validateManifestKind(manifest); err != nil {
		return rleManifest{}, err
	}

	return manifest, nil
}

func readManifestContent(ctx context.Context, path string) ([]byte, error) {
	if isRemoteManifestUrl(path) {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(path)), "https://") {
			return nil, &azdext.LocalError{
				Message:    "Remote RLE manifest URLs must use HTTPS.",
				Code:       "rle_manifest_url_insecure",
				Category:   azdext.LocalErrorCategoryUser,
				Suggestion: "Use an https:// URL or a local manifest path.",
			}
		}
		downloadUrl := normalizeManifestUrl(path)
		requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, downloadUrl, nil)
		if err != nil {
			return nil, err
		}
		resp, err := manifestHTTPClient().Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, &azdext.LocalError{
				Message:  fmt.Sprintf("Failed to download RLE manifest from %s: HTTP %d.", downloadUrl, resp.StatusCode),
				Code:     "rle_manifest_download_failed",
				Category: azdext.LocalErrorCategoryUser,
			}
		}
		const maxManifestBytes = 1 << 20
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
		if err != nil {
			return nil, err
		}
		if len(data) > maxManifestBytes {
			return nil, &azdext.LocalError{
				Message:  fmt.Sprintf("RLE manifest from %s is larger than 1 MiB.", downloadUrl),
				Code:     "rle_manifest_too_large",
				Category: azdext.LocalErrorCategoryUser,
			}
		}
		return data, nil
	}
	return os.ReadFile(path) //nolint:gosec // Manifest path is provided by the user.
}

func manifestHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if !strings.EqualFold(req.URL.Scheme, "https") {
				return &azdext.LocalError{
					Message:    "Remote RLE manifest redirects must stay on HTTPS.",
					Code:       "rle_manifest_url_insecure_redirect",
					Category:   azdext.LocalErrorCategoryUser,
					Suggestion: "Use an HTTPS manifest URL that does not redirect to HTTP.",
				}
			}
			return nil
		},
	}
}

func normalizeManifestUrl(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Host, "github.com") {
		return raw
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 5 || parts[2] != "blob" {
		return raw
	}
	owner, repo, ref := parts[0], parts[1], parts[3]
	path := strings.Join(parts[4:], "/")
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, ref, path)
}

func isRemoteManifestUrl(path string) bool {
	value := strings.ToLower(strings.TrimSpace(path))
	return strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://")
}

func expandManifestEnv(content string) (string, error) {
	missing := map[string]struct{}{}
	expanded := manifestEnvPattern.ReplaceAllStringFunc(content, func(token string) string {
		name := token[2 : len(token)-1]
		value, ok := os.LookupEnv(name)
		if !ok {
			missing[name] = struct{}{}
		}
		return value
	})

	if len(missing) == 0 {
		return expanded, nil
	}

	names := make([]string, 0, len(missing))
	for name := range missing {
		names = append(names, name)
	}
	slices.Sort(names)

	return "", &azdext.LocalError{
		Message:  fmt.Sprintf("RLE manifest references unset environment variable(s): %s.", strings.Join(names, ", ")),
		Code:     "rle_manifest_env_missing",
		Category: azdext.LocalErrorCategoryUser,
		Suggestion: fmt.Sprintf(
			"Set %s, then run the command again.",
			strings.Join(names, ", "),
		),
	}
}

var manifestEnvPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func stateFromManifest(manifest rleManifest) (rleState, error) {
	state := defaultRleState(firstNonEmpty(manifest.Name, manifest.Template.Name))

	state.LocalImage = localImageFromManifest(manifest)
	state.Image = registrationImageFromManifest(manifest)

	return state, nil
}

func localImageFromManifest(manifest rleManifest) string {
	return firstNonEmpty(
		manifest.Template.Local.Image,
		manifest.Template.LocalImage,
		manifest.Local.Image,
		manifest.LocalImage,
		manifest.Template.Image,
		manifest.Image,
		manifest.Template.Environment.Image,
		manifest.Environment.Image,
	)
}

func registrationImageFromManifest(manifest rleManifest) string {
	return firstNonEmpty(
		manifest.Template.Environment.Image,
		manifest.Template.Registration.Image,
		manifest.Template.RegistrationImage,
		manifest.Environment.Image,
		manifest.Registration.Image,
		manifest.RegistrationImage,
		manifest.Template.Image,
		manifest.Image,
	)
}

func validateManifestKind(manifest rleManifest) error {
	kind := strings.ToLower(firstNonEmpty(manifest.Template.Kind, manifest.Kind))
	if kind == "" || kind == "openenv" {
		return nil
	}
	return &azdext.LocalError{
		Message:    fmt.Sprintf("Unsupported RLE manifest kind %q.", kind),
		Code:       "rle_manifest_kind_unsupported",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Use template.kind: openenv.",
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
