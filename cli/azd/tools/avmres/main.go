package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"

	//nolint:ST1001

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	flag "github.com/spf13/pflag"
)

var (
	token      = flag.String("token", "", "GitHub token")
	avmModule  = flag.String("module", "", "AVM module")
	bicepFile  = flag.String("bicep-file", "../../resources/scaffold/templates/resources.bicept", "Bicep file")
	emitSource = flag.Bool("emit-source", false, "Emit the fetched source documents to disk.")
)

var bicepAvmModuleRegex = regexp.MustCompile(`'br/public:(avm/res/.+?)'`)

func run() error {
	var avmModules []string

	if avmModule == nil || *avmModule == "" {
		bytes, err := os.ReadFile(*bicepFile)
		if err != nil {
			return fmt.Errorf("reading bicep file: %w", err)
		}

		matches := bicepAvmModuleRegex.FindAllStringSubmatch(string(bytes), -1)
		for _, match := range matches {
			if len(match) > 1 {
				avmModules = append(avmModules, match[1])
			}
		}
	} else {
		avmModules = append(avmModules, *avmModule)
	}

	resources := []scaffold.ResourceMeta{}
	for _, avmModule := range avmModules {
		moduleVersion := strings.Split(avmModule, ":")
		module, version := moduleVersion[0], moduleVersion[1]

		readme, err := fetchGithub(
			*token,
			"Azure/bicep-registry-modules",
			fmt.Sprintf("%s/README.md", module),
			fmt.Sprintf("%s/%s", module, version))
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

		resources = append(resources, scaffold.ResourceMeta{
			ApiVersion:   apiVersion,
			ResourceType: resourceType,
		})
	}

	slices.SortFunc(resources, func(a, b scaffold.ResourceMeta) int {
		return strings.Compare(a.ResourceType, b.ResourceType)
	})

	for _, res := range resources {
		fmt.Println("{")
		fmt.Printf("  ResourceType: \"%s\",\n", res.ResourceType)
		fmt.Printf("  ApiVersion: \"%s\",\n", res.ApiVersion)
		fmt.Println("},")
	}

	return nil
}

func main() {
	flag.Parse()

	err := run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

// path example: specification/cognitiveservices/resource-manager/Microsoft.CognitiveServices/stable/2023-05-01/cognitiveservices.json
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
		err = os.WriteFile(fileName, content, 0644)
		if err != nil {
			return nil, fmt.Errorf("writing file: %w", err)
		}
	}

	_ = resp.Body.Close()
	return content, nil
}
