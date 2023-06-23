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

var sourceRegex = regexp.MustCompile(`source:\s+'(.+?)'`)

func TestSetup(t *testing.T) {
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
	cloneRepositories(t, repos, "testdata/live")

	root := "testdata/live"
	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	for _, ent := range entries {
		t.Logf("{\"%s\"},", ent.Name())
	}
}

func TestGenerateProject_Live(t *testing.T) {
	root := "testdata/live"
	tests := []struct{ Name string }{
		{"azure-reliable-web-app-pattern-dotnet"},
		{"azure-samples-app-service-javascript-sap-cloud-sdk-quickstart"},
		{"azure-samples-apptemplate-wordpress-on-aca"},
		{"azure-samples-asa-samples-event-driven-application"},
		{"azure-samples-azure-django-postgres-aca"},
		{"azure-samples-azure-health-data-services-toolkit-fhir-function-quickstart"},
		//{"azure-samples-azure-search-openai-demo"},
		// false positive:
		// expected: []repository.service{repository.service{language:"python", path:"app/backend"}}
		// actual  : []repository.service{
		// repository.service{language:"python", path:"app/backend"},
		// repository.service{language:"ts", path:"app/frontend"},
		// repository.service{language:"python", path:"notebooks"}, repository.service{language:"python", path:"scripts"}}

		// {"azure-samples-azure-search-openai-demo-csharp"},
		// false positive:
		// expected: []repository.service{repository.service{language:"dotnet", path:"app/backend"}}
		// actual  : []repository.service{
		// repository.service{language:"dotnet", path:"app/backend"},
		// repository.service{language:"dotnet", path:"app/frontend"},
		// repository.service{language:"dotnet", path:"app/prepdocs/PrepareDocs"},
		// repository.service{language:"python", path:"notebooks"}}

		//{"azure-samples-bindings-dapr-csharp-cron-postgres"},
		// repository has error: language should be csharp
		{"azure-samples-bindings-dapr-nodejs-cron-postgres"},
		{"azure-samples-bindings-dapr-python-cron-postgres"},
		{"azure-samples-chatgpt-quickstart"},
		// {"azure-samples-contoso-real-estate"},
		// detection incorrect: repository has multiple packages.json
		{"azure-samples-fastapi-on-azure-functions"},
		{"azure-samples-function-csharp-ai-textsummarize"},
		{"azure-samples-function-python-ai-textsummarize"},
		{"azure-samples-msdocs-django-postgresql-sample-app"},
		{"azure-samples-msdocs-flask-postgresql-sample-app"},
		{"azure-samples-openai-plugin-fastapi"},
		{"azure-samples-pubsub-dapr-csharp-servicebus"},
		{"azure-samples-pubsub-dapr-nodejs-servicebus"},
		{"azure-samples-pubsub-dapr-python-servicebus"},
		{"azure-samples-react-component-toolkit-openai-demo"},
		{"azure-samples-spring-petclinic-java-mysql"},
		{"azure-samples-svc-invoke-dapr-csharp"},
		{"azure-samples-svc-invoke-dapr-nodejs"},
		{"azure-samples-svc-invoke-dapr-python"},
		// todo apps have src/web specified as "js" instead of "ts"
		{"azure-samples-todo-csharp-cosmos-sql"},
		{"azure-samples-todo-csharp-sql"},
		{"azure-samples-todo-csharp-sql-swa-func"},
		{"azure-samples-todo-java-mongo"},
		// api has both "packages.json" and "pom.xml". should be java, we break the tie giving precedence to pom.xml
		{"azure-samples-todo-java-mongo-aca"},
		{"azure-samples-todo-nodejs-mongo"},
		{"azure-samples-todo-nodejs-mongo-aca"},
		{"azure-samples-todo-nodejs-mongo-aks"},
		{"azure-samples-todo-nodejs-mongo-swa-func"},
		{"azure-samples-todo-nodejs-mongo-terraform"},
		{"azure-samples-todo-python-mongo"},
		{"azure-samples-todo-python-mongo-aca"},
		{"azure-samples-todo-python-mongo-swa-func"},
		{"azure-samples-todo-python-mongo-terraform"},
		{"bradygaster-rockpaperorleans"},
		{"pamelafox-django-quiz-app"},
		{"pamelafox-fastapi-azure-function-apim"},
		{"pamelafox-flask-charts-api-container-app"},
		{"pamelafox-flask-db-quiz-example"},
		{"pamelafox-flask-gallery-container-app"},
		{"pamelafox-flask-surveys-container-app"},
		{"pamelafox-simple-fastapi-container"},
		{"pamelafox-simple-flask-api-container"},
		{"pamelafox-staticmaps-function"},
		//{"rpothin-servicebus-csharp-function-dataverse"},
		// incorrect detection:
		{"sabbour-aks-app-template"},
		{"savannahostrowski-jupyter-mercury-aca"},
		{"tonybaloney-django-on-azure"},
		{"tonybaloney-simple-flask-azd"},
	}
	for _, tt := range tests {
		if strings.Contains(tt.Name, "sabbour-aks-app-template") {
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
		return fmt.Errorf("failed to clone repository: %s\nOutput: %s", err, output)
	}

	return nil
}
