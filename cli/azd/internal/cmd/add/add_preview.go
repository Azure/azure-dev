// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/azure/azure-dev/internal/scaffold"
	"github.com/azure/azure-dev/pkg/environment"
	"github.com/azure/azure-dev/pkg/infra/provisioning"
	"github.com/azure/azure-dev/pkg/input"
	"github.com/azure/azure-dev/pkg/output"
	"github.com/azure/azure-dev/pkg/project"
	"github.com/fatih/color"
)

type metaDisplay struct {
	ResourceType string
	Variables    []string
}

func Metadata(r *project.ResourceConfig) metaDisplay {
	azureResType := r.Type.AzureResourceType()

	for _, res := range scaffold.Resources {
		if res.ResourceType == azureResType {
			// transform to standard variables
			prefix := res.StandardVarPrefix

			if r.Existing {
				prefix += "_" + environment.Key(r.Name)
			}

			// host resources are special and prefixed with the name
			if strings.HasPrefix(string(r.Type), "host.") {
				prefix = strings.ToUpper(r.Name)
			}

			variables := scaffold.EnvVars(prefix, res.Variables)
			displayVariables := slices.Sorted(maps.Keys(variables))

			display := metaDisplay{
				ResourceType: res.ResourceType,
				Variables:    displayVariables,
			}

			return display
		}
	}

	return metaDisplay{}
}

func (a *AddAction) previewProvision(
	ctx context.Context,
	prjConfig *project.ProjectConfig,
	resourcesToAdd []*project.ResourceConfig,
	usedBy []string,
) error {
	a.console.ShowSpinner(ctx, "Previewing changes....", input.Step)
	err := provisioning.EnsureSubscriptionAndLocation(
		ctx, a.envManager, a.env, a.prompter, provisioning.EnsureSubscriptionAndLocationOptions{})
	if err != nil {
		return err
	}

	environmentDetails, err := getEnvDetails(ctx, a.env, a.subManager)
	if err != nil {
		log.Printf("failed getting environment details: %s", err)
	}

	a.console.Message(ctx, fmt.Sprintf("\n%s\n", output.WithBold("Previewing Azure resource changes")))
	a.console.Message(ctx, "Environment: "+output.WithHighLightFormat(a.env.Name()))

	if environmentDetails.Subscription != "" {
		a.console.MessageUxItem(ctx, &environmentDetails)
	}

	a.console.StopSpinner(ctx, "", input.StepDone)

	a.console.Message(ctx, fmt.Sprintf("%s\n", output.WithBold("Resources")))

	previewWriter := previewWriter{w: a.console.GetWriter()}
	w := tabwriter.NewWriter(&previewWriter, 0, 0, 5, ' ', 0)

	fmt.Fprintln(w, "b  Name\tResource type")
	for _, res := range resourcesToAdd {
		meta := Metadata(res)
		status := ""
		if res.Existing {
			status = " (existing)"
		}

		fmt.Fprintf(w, "+  %s\t%s%s\n", res.Name, meta.ResourceType, status)
	}

	w.Flush()
	a.console.Message(ctx, fmt.Sprintf("\n%s\n", output.WithBold("Variables")))

	for _, res := range resourcesToAdd {
		if strings.HasPrefix(string(res.Type), "host.") {
			for _, use := range res.Uses {
				if usingRes, ok := prjConfig.Resources[use]; ok {
					fmt.Fprintf(w, "   %s -> %s\n", res.Name, output.WithBold("%s", use))

					meta := Metadata(usingRes)
					for _, envVar := range meta.Variables {
						fmt.Fprintf(w, "g   + %s\n", envVar)
					}

					fmt.Fprintln(w)
				}
			}
		} else {
			meta := Metadata(res)

			for _, usedBy := range usedBy {
				fmt.Fprintf(w, "   %s -> %s\n", usedBy, output.WithBold("%s", res.Name))

				for _, envVar := range meta.Variables {
					fmt.Fprintf(w, "g   + %s\n", envVar)
				}

				fmt.Fprintln(w)
			}
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
