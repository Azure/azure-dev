// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/azure/azure-dev/pkg/output"
	flag "github.com/spf13/pflag"
)

var (
	token      = flag.String("token", "", "GitHub token")
	bicepDir   = flag.String("bicep-dir", "../../resources/scaffold", "Bicep dir")
	emitSource = flag.Bool("emit-source", false, "Emit the fetched source documents to disk.")
)

var bicepAvmModuleRegex = regexp.MustCompile(`module\s+(.+?)\s+'br/public:(avm/res/.+?)'`)

type avmModuleReference struct {
	BicepName     string
	ResourceType  string
	ApiVersion    string
	ModulePath    string
	ModuleVersion string
	BicepFile     string
}

func run() error {
	var avmModules []*avmModuleReference

	err := filepath.WalkDir(*bicepDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		if filepath.Ext(path) != ".bicep" && filepath.Ext(path) != ".bicept" {
			return nil
		}

		bytes, err := os.ReadFile(filepath.Join(*bicepDir, path))
		if err != nil {
			return fmt.Errorf("reading bicep file: %w", err)
		}

		matches := bicepAvmModuleRegex.FindAllStringSubmatch(string(bytes), -1)
		for _, match := range matches {
			if len(match) > 2 {
				pathAndVersion := strings.Split(match[2], ":")

				module := &avmModuleReference{
					BicepName:     match[1],
					ModulePath:    pathAndVersion[0],
					ModuleVersion: pathAndVersion[1],
					BicepFile:     path,
				}
				avmModules = append(avmModules, module)
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("walking bicep dir: %w", err)
	}

	for _, avmModule := range avmModules {
		readme, err := fetchGithub(
			*token,
			"Azure/bicep-registry-modules",
			fmt.Sprintf("%s/README.md", avmModule.ModulePath),
			fmt.Sprintf("%s/%s", avmModule.ModulePath, avmModule.ModuleVersion))
		if err != nil {
			return fmt.Errorf("fetching README: %w", err)
		}

		brackets := regexp.MustCompile(`\[(.*?)\]`)

		lines := strings.Split(string(readme), "\n")
		resourceType := ""
		apiVersion := ""
		inSection := false
		for i, line := range lines {
			if i == 0 {
				// Extract the resource type from something that looks like:
				// # Storage Accounts `[Microsoft.Storage/storageAccounts]`
				matches := brackets.FindStringSubmatch(line)
				if len(matches) > 1 {
					resourceType = matches[1]
				}
			}

			if strings.HasPrefix(line, "## Resource Types") {
				inSection = true
			}

			if inSection {
				// Example input:
				//nolint:lll
				// | `Microsoft.Storage/storageAccounts` | [2023-05-01](https://learn.microsoft.com/en-us/azure/templates/Microsoft.Storage/2023-05-01/storageAccounts) |
				if strings.HasPrefix(line, "|") { // inside table
					if strings.Contains(line, "`"+resourceType+"`") { // found the resource type
						matches := brackets.FindStringSubmatch(line)
						if len(matches) > 1 {
							apiVersion = matches[1]
							break
						}
					}
				}
			}
		}

		if resourceType == "" || apiVersion == "" {
			return fmt.Errorf("unable to find resource type or API version, run with --emit-source to see the source")
		}

		avmModule.ResourceType = resourceType
		avmModule.ApiVersion = apiVersion
	}

	slices.SortFunc(avmModules, func(a, b *avmModuleReference) int {
		return strings.Compare(a.BicepName, b.BicepName)
	})

	formatter := output.TableFormatter{}
	err = formatter.Format(avmModules, os.Stdout, tableInputOptions)
	if err != nil {
		return fmt.Errorf("formatting output: %w", err)
	}

	return nil
}

var tableInputOptions = output.TableFormatterOptions{
	Columns: []output.Column{
		{
			Heading:       "BicepName",
			ValueTemplate: "{{.BicepName}}",
		},
		{
			Heading:       "ResourceType",
			ValueTemplate: "{{.ResourceType}}",
		},
		{
			Heading:       "ApiVersion",
			ValueTemplate: "{{.ApiVersion}}",
		},
		{
			Heading:       "ModulePath",
			ValueTemplate: "{{.ModulePath}}",
		},
		{
			Heading:       "ModuleVersion",
			ValueTemplate: "{{.ModuleVersion}}",
		},
		// {
		// 	Heading:       "BicepFile",
		// 	ValueTemplate: "{{.BicepFile}}",
		// 	Transformer:   filepath.Base,
		// },
	},
}

func main() {
	flag.Parse()

	err := run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

// path example:
// specification/cognitiveservices/resource-manager/Microsoft.CognitiveServices/stable/2023-05-01/cognitiveservices.json
// token example: GITHUB_TOKEN
func fetchGithub(
	token string,
	repo string,
	path string,
	tag string) ([]byte, error) {
	req, err := http.NewRequest("GET",
		fmt.Sprintf("https://api.github.com/repos/%s/contents/%s?ref=%s", repo, path, tag), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.raw")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	if token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("http status code: %d", resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading body: %w", err)
	}

	if emitSource != nil && *emitSource {
		lastSep := strings.LastIndex(path, "/")
		fileName := path[lastSep+1:]
		err = os.WriteFile(fileName, content, 0600)
		if err != nil {
			return nil, fmt.Errorf("writing file: %w", err)
		}
	}

	_ = resp.Body.Close()
	return content, nil
}
