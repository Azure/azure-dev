package repository

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slices"
)

func TestGenerateProject_Live(t *testing.T) {
	if os.Getenv("AZD_TEST_APP_GENERATE_LIVE") == "" {
		t.Skip("skip live test")
	}

	root := "testdata/live"
	newTemplates := Discover(t, root)

	tests := []struct {
		Name       string
		Suppressed bool
	}{
		{Name: "azure-reliable-web-app-pattern-dotnet"},
		{Name: "azure-samples-app-service-javascript-sap-cloud-sdk-quickstart"},
		{Name: "azure-samples-apptemplate-wordpress-on-aca"},
		{Name: "azure-samples-asa-samples-event-driven-application"},
		{Name: "azure-samples-azure-django-postgres-aca"},
		{Name: "azure-samples-azure-health-data-services-toolkit-fhir-function-quickstart"},
		{Name: "azure-samples-azure-search-openai-demo", Suppressed: true},
		// false positive
		// expected: []repository.service{repository.service{language:"python", path:"app/backend"}}
		// actual  : []repository.service{
		// repository.service{language:"python", path:"app/backend"},
		// repository.service{language:"ts", path:"app/frontend"},
		// repository.service{language:"python", path:"notebooks"}, repository.service{language:"python", path:"scripts"}}

		{Name: "azure-samples-azure-search-openai-demo-csharp", Suppressed: true},
		// false positive
		// expected: []repository.service{repository.service{language:"dotnet", path:"app/backend"}}
		// actual  : []repository.service{
		// repository.service{language:"dotnet", path:"app/backend"},
		// repository.service{language:"dotnet", path:"app/frontend"},
		// repository.service{language:"dotnet", path:"app/prepdocs/PrepareDocs"},
		// repository.service{language:"python", path:"notebooks"}}

		{Name: "azure-samples-bindings-dapr-csharp-cron-postgres", Suppressed: true},
		// repository error: language should be csharp
		{Name: "azure-samples-bindings-dapr-nodejs-cron-postgres"},
		{Name: "azure-samples-bindings-dapr-python-cron-postgres"},
		{Name: "azure-samples-chatgpt-quickstart"},
		{Name: "azure-samples-contoso-real-estate", Suppressed: true},
		// detection incorrect: repository has multiple packages.json
		{Name: "azure-samples-fastapi-on-azure-functions"},
		{Name: "azure-samples-function-csharp-ai-textsummarize"},
		{Name: "azure-samples-function-python-ai-textsummarize"},
		{Name: "azure-samples-msdocs-django-postgresql-sample-app"},
		{Name: "azure-samples-msdocs-flask-postgresql-sample-app"},
		{Name: "azure-samples-openai-plugin-fastapi"},
		{Name: "azure-samples-pubsub-dapr-csharp-servicebus"},
		{Name: "azure-samples-pubsub-dapr-nodejs-servicebus"},
		{Name: "azure-samples-pubsub-dapr-python-servicebus"},
		{Name: "azure-samples-react-component-toolkit-openai-demo"},
		{Name: "azure-samples-spring-petclinic-java-mysql"},
		{Name: "azure-samples-svc-invoke-dapr-csharp"},
		{Name: "azure-samples-svc-invoke-dapr-nodejs"},
		{Name: "azure-samples-svc-invoke-dapr-python"},
		// todo apps have src/web specified as "js" instead of "ts"
		// this is fixed using custom logic in the comparison below
		{Name: "azure-samples-todo-csharp-cosmos-sql"},
		{Name: "azure-samples-todo-csharp-sql"},
		{Name: "azure-samples-todo-csharp-sql-swa-func"},
		{Name: "azure-samples-todo-java-mongo"},
		// api has both "packages.json" and "pom.xml". should be java, we break the tie giving precedence to pom.xml
		{Name: "azure-samples-todo-java-mongo-aca"},
		{Name: "azure-samples-todo-nodejs-mongo"},
		{Name: "azure-samples-todo-nodejs-mongo-aca"},
		{Name: "azure-samples-todo-nodejs-mongo-aks"},
		{Name: "azure-samples-todo-nodejs-mongo-swa-func"},
		{Name: "azure-samples-todo-nodejs-mongo-terraform"},
		{Name: "azure-samples-todo-python-mongo"},
		{Name: "azure-samples-todo-python-mongo-aca"},
		{Name: "azure-samples-todo-python-mongo-swa-func"},
		{Name: "azure-samples-todo-python-mongo-terraform"},
		{Name: "bradygaster-rockpaperorleans"},
		{Name: "pamelafox-django-quiz-app"},
		{Name: "pamelafox-fastapi-azure-function-apim"},
		{Name: "pamelafox-flask-charts-api-container-app"},
		{Name: "pamelafox-flask-db-quiz-example"},
		{Name: "pamelafox-flask-gallery-container-app"},
		{Name: "pamelafox-flask-surveys-container-app"},
		{Name: "pamelafox-simple-fastapi-container"},
		{Name: "pamelafox-simple-flask-api-container"},
		{Name: "pamelafox-staticmaps-function"},
		{Name: "rpothin-servicebus-csharp-function-dataverse", Suppressed: true},
		// incorrect detection: doesn't handle functionapp
		{Name: "sabbour-aks-app-template", Suppressed: true},
		// false positive: sabbour-aks-app-template has placeholder app
		{Name: "savannahostrowski-jupyter-mercury-aca"},
		{Name: "tonybaloney-django-on-azure"},
		{Name: "tonybaloney-simple-flask-azd"},
	}

	existingTests := make(map[string]struct{}, len(tests))
	for _, tt := range tests {
		existingTests[tt.Name] = struct{}{}
	}

	for _, template := range newTemplates {
		if _, ok := existingTests[template]; !ok {
			t.Errorf("new template: %s", template)
		}
	}

	for _, tt := range tests {
		if tt.Suppressed {
			continue
		}

		t.Run(tt.Name, func(t *testing.T) {
			dir := filepath.Join(root, tt.Name)
			err := GenerateProject(dir)
			require.NoError(t, err)

			expectedPrj, err := project.Load(context.Background(), filepath.Join(dir, "azure.yaml"))
			require.NoError(t, err)

			actualPrj, err := project.Load(context.Background(), filepath.Join(dir, "azure.yaml.gen"))
			require.NoError(t, err)

			type service struct {
				language string
				path     string
			}

			expected := make([]service, 0, len(expectedPrj.Services))
			for _, svc := range expectedPrj.Services {
				if svc.Language == project.ServiceLanguageCsharp ||
					svc.Language == project.ServiceLanguageFsharp {
					svc.Language = project.ServiceLanguageDotNet
				}

				if strings.HasPrefix(expectedPrj.Name, "todo-") {
					if svc.Language == project.ServiceLanguageJavaScript {
						svc.Language = project.ServiceLanguageTypeScript
					}
				}

				expected = append(expected, service{
					language: string(svc.Language),
					path:     filepath.Clean(svc.RelativePath),
				})
			}
			slices.SortFunc(expected, func(a, b service) bool {
				return a.path < b.path
			})

			actual := make([]service, 0, len(actualPrj.Services))
			for _, svc := range actualPrj.Services {
				actual = append(actual, service{
					language: string(svc.Language),
					path:     filepath.Clean(svc.RelativePath),
				})
			}
			slices.SortFunc(actual, func(a, b service) bool {
				return a.path < b.path
			})

			assert.Equal(t, expected, actual)
		})
	}
}

var sourceRegex = regexp.MustCompile(`source:\s+'(.+?)'`)

// Discovers templates to use for testing, cloning each template repository under root.
// Currently, this uses the current list of awesome-azd templates.
func Discover(t *testing.T, root string) []string {
	resp, err := http.Get("https://raw.githubusercontent.com/Azure/awesome-azd/main/website/src/data/users.tsx")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GitHub API returned non-200 status code: %s", resp.Status)
	}

	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	matches := sourceRegex.FindAllStringSubmatch(string(bytes), -1)
	if matches == nil {
		t.Fatal("found no matches")
	}

	repos := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) != 2 {
			panic("invalid match")
		}
		repos = append(repos, match[1])
	}

	slices.Sort(repos)
	cloneRepositories(t, repos, root)

	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, ent := range entries {
		names = append(names, ent.Name())
	}

	return names
}

type CloneJob struct {
	CloneURL  string
	TargetDir string
}

func cloneRepositories(t *testing.T, repositories []string, rootDirectory string) {
	err := os.MkdirAll(rootDirectory, 0755)
	if err != nil {
		t.Fatal(err)
	}
	jobs := make(chan CloneJob, len(repositories))
	results := make(chan bool, len(repositories))

	for i := 0; i < 15; i++ {
		go cloneWorker(t, jobs, results)
	}

	host := "https://github.com/"
	for _, repo := range repositories {
		name := repo[len(host):]
		name = strings.ToLower(name)
		name = strings.TrimRight(name, "/")
		name = strings.ReplaceAll(name, "/", "-")

		// Create the target directory for cloning
		targetDir := filepath.Join(rootDirectory, name)

		t.Logf("repo: %s", name)

		// Check if the directory already exists
		if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
			t.Logf("Skipping %s: Directory already exists\n", name)
			results <- true
			continue
		}

		// Add clone job to the queue
		jobs <- CloneJob{CloneURL: repo, TargetDir: targetDir}
	}

	close(jobs)

	for i := 0; i < len(repositories); i++ {
		<-results
	}
	close(results)
}

func cloneWorker(t *testing.T, jobs <-chan CloneJob, results chan<- bool) {
	for job := range jobs {
		err := cloneRepository(job.CloneURL, job.TargetDir)
		if err != nil {
			panic(fmt.Sprintf("Error cloning %s: %s\n", job.CloneURL, err))
		} else {
			t.Logf("Cloned %s\n", job.CloneURL)
		}

		results <- true
	}
}

func cloneRepository(cloneURL, targetDir string) error {
	cmd := exec.Command("git", "clone", cloneURL, targetDir)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w\nOutput: %s", err, output)
	}

	return nil
}
