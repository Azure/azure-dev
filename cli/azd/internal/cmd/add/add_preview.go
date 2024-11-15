package add

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"text/tabwriter"

	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/fatih/color"
)

// resourceMeta contains metadata of the resource
type resourceMeta struct {
	// The underlying resource type.
	AzureResourceType string
	// UseEnvVars is the list of environment variable names that would be populated when this resource is used
	UseEnvVars []string
}

func Metadata(r *project.ResourceConfig) resourceMeta {
	res := resourceMeta{}

	// These are currently duplicated, static values maintained separately from the backend generation files
	// If updating resources.bicep, these values should also be updated.
	switch r.Type {
	case project.ResourceTypeHostContainerApp:
		res.AzureResourceType = "Microsoft.App/containerApps"
		res.UseEnvVars = []string{
			strings.ToUpper(r.Name) + "_BASE_URL",
		}
	case project.ResourceTypeDbRedis:
		res.AzureResourceType = "Microsoft.Cache/redis"
		res.UseEnvVars = []string{
			"REDIS_HOST",
			"REDIS_PORT",
			"REDIS_ENDPOINT",
			"REDIS_PASSWORD",
			"REDIS_URL",
		}
	case project.ResourceTypeDbPostgres:
		res.AzureResourceType = "Microsoft.DBforPostgreSQL/flexibleServers/databases"
		res.UseEnvVars = []string{
			"POSTGRES_HOST",
			"POSTGRES_USERNAME",
			"POSTGRES_DATABASE",
			"POSTGRES_PASSWORD",
			"POSTGRES_PORT",
			"POSTGRES_URL",
		}
	case project.ResourceTypeDbMongo:
		res.AzureResourceType = "Microsoft.DocumentDB/databaseAccounts/mongodbDatabases"
		res.UseEnvVars = []string{
			"MONGODB_URL",
		}
	case project.ResourceTypeOpenAiModel:
		res.AzureResourceType = "Microsoft.CognitiveServices/accounts/deployments"
		res.UseEnvVars = []string{
			"AZURE_OPENAI_ENDPOINT",
		}
	}
	return res
}

func (a *AddAction) previewProvision(
	ctx context.Context,
	prjConfig *project.ProjectConfig,
	resourceToAdd *project.ResourceConfig,
	usedBy []string,
) error {
	a.console.ShowSpinner(ctx, "Previewing changes....", input.Step)
	err := provisioning.EnsureSubscriptionAndLocation(ctx, a.envManager, a.env, a.prompter, nil)
	if err != nil {
		return err
	}

	environmentDetails, err := getEnvDetails(ctx, a.env, a.subManager)
	if err != nil {
		log.Printf("failed getting environment details: %s", err)
	}

	a.console.Message(ctx, fmt.Sprintf("\n%s\n", output.WithBold("Previewing Azure resource changes")))
	a.console.Message(ctx, "Environment: "+color.BlueString(a.env.Name()))

	if environmentDetails.Subscription != "" {
		a.console.MessageUxItem(ctx, &environmentDetails)
	}

	a.console.StopSpinner(ctx, "", input.StepDone)

	a.console.Message(ctx, fmt.Sprintf("%s\n", output.WithBold("Resources")))

	previewWriter := previewWriter{w: a.console.GetWriter()}
	w := tabwriter.NewWriter(&previewWriter, 0, 0, 5, ' ', 0)

	fmt.Fprintln(w, "b  Name\tResource type")
	meta := Metadata(resourceToAdd)
	fmt.Fprintf(w, "+  %s\t%s\n", resourceToAdd.Name, meta.AzureResourceType)

	w.Flush()
	a.console.Message(ctx, fmt.Sprintf("\n%s\n", output.WithBold("Environment variables")))

	if strings.HasPrefix(string(resourceToAdd.Type), "host.") {
		for _, use := range resourceToAdd.Uses {
			if res, ok := prjConfig.Resources[use]; ok {
				fmt.Fprintf(w, "   %s -> %s\n", resourceToAdd.Name, output.WithBold("%s", use))

				meta := Metadata(res)
				for _, envVar := range meta.UseEnvVars {
					fmt.Fprintf(w, "g   + %s\n", envVar)
				}

				fmt.Fprintln(w)
			}
		}
	} else {
		meta := Metadata(resourceToAdd)

		for _, usedBy := range usedBy {
			fmt.Fprintf(w, "   %s -> %s\n", usedBy, output.WithBold("%s", resourceToAdd.Name))

			for _, envVar := range meta.UseEnvVars {
				fmt.Fprintf(w, "g   + %s\n", envVar)
			}

			fmt.Fprintln(w)
		}
	}

	a.console.Message(ctx, "")
	return nil
}

// previewWriter applies text transformations on preview text before writing to standard output.
// A control character can be specified at the start of each line to apply transformations.
//
// Current control character transformations:
//   - '+' -> the line is colored green
//   - '-' -> the line is colored red
//   - 'b' -> the line is bolded; this character is replaced with a space
//   - 'g' -> the line is colored green; this character is replaced with a space
type previewWriter struct {
	// the underlying writer to write to
	w io.Writer

	// buffer for the current line
	buf bytes.Buffer
	// stores the current line start character
	lineStartChar rune
}

// Write implements the io.Writer interface
func (pw *previewWriter) Write(p []byte) (n int, err error) {
	for i, b := range p {
		if pw.buf.Len() == 0 && len(p) > 0 {
			pw.lineStartChar = rune(p[0])

			if pw.lineStartChar == 'b' || pw.lineStartChar == 'g' {
				// hidden characters, replace with a space
				b = ' '
			}
		}

		if err := pw.buf.WriteByte(b); err != nil {
			return i, err
		}

		if b == '\n' {
			transform := fmt.Sprintf
			switch pw.lineStartChar {
			case '+', 'g':
				transform = color.GreenString
			case '-':
				transform = color.RedString
			case 'b':
				transform = output.WithBold
			}

			_, err := pw.w.Write([]byte(transform(pw.buf.String())))
			if err != nil {
				return i, err
			}

			pw.buf.Reset()
			continue
		}
	}

	return len(p), nil
}
