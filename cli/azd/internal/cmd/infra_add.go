package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/braydonk/yaml"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func NewInfraAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add",
		Short: "Add a component to your app.",
	}
}

type AddAction struct {
	azdCtx  *azdcontext.AzdContext
	console input.Console
}

func (a *AddAction) Run(ctx context.Context) (*actions.ActionResult, error) {
	prjConfig, err := project.Load(ctx, a.azdCtx.ProjectPath())
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}

	continueOption, err := a.console.Select(ctx, input.ConsoleOptions{
		Message: "What would you like to add?",
		Options: []string{"Database", "Storage", "Messaging", "AI Service"},
	})
	if err != nil {
		return nil, err
	}

	if continueOption != 0 {
		return nil, fmt.Errorf("not implemented")
	}

	resourceTypes := project.AllResources()
	resourceTypesDisplay := make([]string, 0, len(resourceTypes))
	resourceTypesDisplayMap := make(map[string]project.ResourceType)
	for _, resourceType := range resourceTypes {
		resourceTypesDisplay = append(resourceTypesDisplay, resourceType.String())
		resourceTypesDisplayMap[resourceType.String()] = resourceType
	}
	slices.Sort(resourceTypesDisplay)

	dbOption, err := a.console.Select(ctx, input.ConsoleOptions{
		Message: "Which type of database?",
		Options: resourceTypesDisplay,
	})
	if err != nil {
		return nil, err
	}

	resourceToAdd := &project.ResourceConfig{
		Type: resourceTypesDisplayMap[resourceTypesDisplay[dbOption]],
	}

	svc := make([]string, 0, len(prjConfig.Services))
	for _, service := range prjConfig.Services {
		svc = append(svc, service.Name)
	}
	slices.Sort(svc)

	svcOptions, err := a.console.MultiSelect(ctx, input.ConsoleOptions{
		Message: "Select the service(s) that uses this database",
		Options: svc,
	})
	if err != nil {
		return nil, err
	}

	configureRes, err := a.Configure(ctx, resourceToAdd)
	if err != nil {
		return nil, err
	}

	resourceNode, err := EncodeAsYamlNode(map[string]*project.ResourceConfig{resourceToAdd.Name: resourceToAdd})
	if err != nil {
		panic(fmt.Sprintf("encoding yaml node: %v", err))
	}

	file, err := os.OpenFile(a.azdCtx.ProjectPath(), os.O_RDWR, osutil.PermissionFile)
	if err != nil {
		return nil, fmt.Errorf("reading project file: %w", err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)

	var doc yaml.Node
	err = decoder.Decode(&doc)
	if err != nil {
		return nil, fmt.Errorf("failed to decode: %w", err)
	}

	err = AppendNode(&doc, "resources?", resourceNode)
	if err != nil {
		return nil, fmt.Errorf("updating resources: %w", err)
	}

	for _, svc := range svcOptions {
		err = AppendNode(&doc, fmt.Sprintf("services.%s.uses[]?", svc), &yaml.Node{
			Kind:  yaml.ScalarNode,
			Value: resourceToAdd.Name,
		})
		if err != nil {
			return nil, fmt.Errorf("updating services: %w", err)
		}
	}

	// Write modified YAML back to file
	err = file.Truncate(0)
	if err != nil {
		return nil, fmt.Errorf("truncating file: %w", err)
	}
	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("seeking to start of file: %w", err)
	}

	indentation := CalcIndentation(&doc)
	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(indentation)
	encoder.SetAssumeBlockAsLiteral(true)
	// encoder.SetIndentlessBlockSequence(true)

	err = encoder.Encode(&doc)
	if err != nil {
		return nil, fmt.Errorf("failed to encode: %w", err)
	}

	err = file.Close()
	if err != nil {
		return nil, fmt.Errorf("closing file: %w", err)
	}

	var followUp string
	defaultFollowUp := "You can run '" + color.BlueString("azd provision") + "' to provision these infrastructure changes."
	if len(svcOptions) > 0 {
		followUp = "The following environment variables will be set in " +
			strings.Join(svcOptions, ", ") + ":\n\n"
		for _, envVar := range configureRes.ConnectionEnvVars {
			followUp += "  - " + envVar + "\n"
		}
		followUp += "\n" + defaultFollowUp + "\n" + "You may also run '" +
			color.BlueString("azd show <service> env") +
			"' to show environment variables of the currently provisioned instance."
	} else {
		followUp = defaultFollowUp
	}

	return &actions.ActionResult{
		Message: &actions.ResultMessage{
			Header:   "azure.yaml has been updated to include the new resource.",
			FollowUp: followUp,
		},
	}, err
}

func NewInfraAddAction(
	azdCtx *azdcontext.AzdContext,
	console input.Console) actions.Action {
	return &AddAction{
		azdCtx:  azdCtx,
		console: console,
	}
}

type configureResult struct {
	ConnectionEnvVars []string
}

func (a *AddAction) Configure(ctx context.Context, r *project.ResourceConfig) (configureResult, error) {
	if r.Type == project.ResourceTypeDbRedis {
		r.Name = "redis"
		// this can be moved to central location for resource types
		return configureResult{
			ConnectionEnvVars: []string{
				"REDIS_HOST",
				"REDIS_PORT",
				"REDIS_ENDPOINT",
				"REDIS_PASSWORD",
			},
		}, nil
	}

	dbName, err := a.console.Prompt(ctx, input.ConsoleOptions{
		Message: fmt.Sprintf("Input the name of the app database (%s)", r.Type.String()),
		Help: "Hint: App database name\n\n" +
			"Name of the database that the app connects to. " +
			"This database will be created after running azd provision or azd up.",
	})
	if err != nil {
		return configureResult{}, err
	}

	r.Name = dbName

	res := configureResult{}
	switch r.Type {
	case project.ResourceTypeDbPostgres:
		res.ConnectionEnvVars = []string{
			"POSTGRES_HOST",
			"POSTGRES_USERNAME",
			"POSTGRES_DATABASE",
			"POSTGRES_PASSWORD",
			"POSTGRES_PORT",
		}
	case project.ResourceTypeDbMongo:
		res.ConnectionEnvVars = []string{
			"AZURE_COSMOS_MONGODB_CONNECTION_STRING",
		}
	}
	return res, nil
}

func EncodeAsYamlNode(v interface{}) (*yaml.Node, error) {
	var node yaml.Node
	err := node.Encode(v)
	if err != nil {
		return nil, fmt.Errorf("encoding yaml node: %w", err)
	}

	// By default, the node will be a document node that represents a YAML document,
	// but we are only interested in the content of the document.
	return &node, nil
}

func AppendNode(root *yaml.Node, path string, node *yaml.Node) error {
	parts := strings.Split(path, ".")
	return modifyNodeRecursive(root, parts, node)
}

func modifyNodeRecursive(current *yaml.Node, parts []string, node *yaml.Node) error {
	if len(parts) == 0 {
		return appendNode(current, node)
	}

	optional := strings.HasSuffix(parts[0], "?")
	seek := strings.TrimSuffix(parts[0], "?")

	isArr := strings.HasSuffix(seek, "[]")
	seek = strings.TrimSuffix(seek, "[]")

	switch current.Kind {
	case yaml.DocumentNode:
		return modifyNodeRecursive(current.Content[0], parts, node)
	case yaml.MappingNode:
		for i := 0; i < len(current.Content); i += 2 {
			if current.Content[i].Value == seek {
				return modifyNodeRecursive(current.Content[i+1], parts[1:], node)
			}
		}
	case yaml.SequenceNode:
		index, err := strconv.Atoi(seek)
		if err != nil {
			return err
		}
		if index >= 0 && index < len(current.Content) {
			return modifyNodeRecursive(current.Content[index], parts[1:], node)
		}
	}

	if optional {
		current.Content = append(current.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: seek})
		if isArr {
			current.Content = append(current.Content, &yaml.Node{
				Kind:    yaml.SequenceNode,
				Content: []*yaml.Node{},
			})
		} else {
			current.Content = append(current.Content, &yaml.Node{
				Kind:    yaml.MappingNode,
				Content: []*yaml.Node{},
			})
		}

		return modifyNodeRecursive(current.Content[len(current.Content)-1], parts[1:], node)
	}

	return fmt.Errorf("path not found: %s", strings.Join(parts, "."))
}

func appendNode(current *yaml.Node, node *yaml.Node) error {
	// get the content of the node to append
	contents := []*yaml.Node{}
	switch node.Kind {
	case yaml.MappingNode, yaml.SequenceNode, yaml.DocumentNode:
		contents = append(contents, node.Content...)
	case yaml.ScalarNode:
		contents = append(contents, node)
	default:
		return fmt.Errorf("cannot append node of kind %d", node.Kind)
	}

	switch current.Kind {
	case yaml.MappingNode:
		current.Content = append(current.Content, contents...)
	case yaml.SequenceNode:
		current.Content = append(current.Content, contents...)
	default:
		return fmt.Errorf("cannot append to node of kind %d", current.Kind)
	}
	return nil
}

// CalcIndentation calculates the indentation level of the first mapping node in the document.
// If the document does not contain a mapping node that is indented, it returns 2.
func CalcIndentation(doc *yaml.Node) int {
	var curr *yaml.Node
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		curr = doc.Content[0]
	}

	if curr.Kind == yaml.MappingNode {
		for i := 0; i < len(curr.Content); i += 2 {
			if curr.Content[i+1].Kind == yaml.MappingNode &&
				curr.Content[i+1].Line > curr.Content[i].Line &&
				curr.Content[i+1].Column > curr.Content[i].Column {
				return curr.Content[i+1].Column - curr.Content[i].Column
			}
		}
	}

	return 2
}
