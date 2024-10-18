package workflow

import (
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/require"
)

var testWorkflow = &Workflow{
	Name: "up",
	Steps: []*Step{
		{AzdCommand: Command{Args: []string{"package", "--all"}}},
		{AzdCommand: Command{Args: []string{"provision"}}},
		{AzdCommand: Command{Args: []string{"deploy", "--all"}}},
	},
}

func Test_WorkflowMap_MarshalYAML(t *testing.T) {
	workflowMap := WorkflowMap{}
	workflowMap["up"] = testWorkflow
	workflowMap["down"] = testWorkflow

	expected, err := yaml.Marshal(workflowMap)
	require.NoError(t, err)

	actual, err := yaml.Marshal(workflowMap)
	require.NoError(t, err)

	require.NoError(t, err)
	require.Equal(t, expected, actual)
}

func Test_WorkflowMap_UnmarshallYAML(t *testing.T) {
	t.Run("array style", func(t *testing.T) {
		var wm WorkflowMap
		yamlString := heredoc.Doc(`
			up:
			  - azd: package --all
			  - azd: provision
			  - azd: deploy --all
		`)

		err := yaml.Unmarshal([]byte(yamlString), &wm)
		require.NoError(t, err)

		upWorkflow, ok := wm["up"]
		require.True(t, ok)

		assertWorkflow(t, upWorkflow)
	})

	t.Run("map style", func(t *testing.T) {
		var workflowMap WorkflowMap
		yamlString := heredoc.Doc(`
			up:
			  steps:
			    - azd: package --all
			    - azd: provision
			    - azd: deploy --all
		`)

		err := yaml.Unmarshal([]byte(yamlString), &workflowMap)
		require.NoError(t, err)

		upWorkflow, ok := workflowMap["up"]
		require.True(t, ok)

		assertWorkflow(t, upWorkflow)
	})

	t.Run("verbose style", func(t *testing.T) {
		var workflowMap WorkflowMap
		yamlString := heredoc.Doc(`
			up:
			  steps:
			    - azd:
			        args:
			          - package
			          - --all
			    - azd:
			        args:
			          - provision
			    - azd:
			        args:
			          - deploy
			          - --all
		`)

		err := yaml.Unmarshal([]byte(yamlString), &workflowMap)
		require.NoError(t, err)

		upWorkflow, ok := workflowMap["up"]
		require.True(t, ok)

		assertWorkflow(t, upWorkflow)
	})

	t.Run("invalid workflow", func(t *testing.T) {
		var workflowMap WorkflowMap
		yamlString := heredoc.Doc(`
			up: provision && deploy --all && package --all
		`)

		err := yaml.Unmarshal([]byte(yamlString), &workflowMap)
		require.Error(t, err)
	})
}

func assertWorkflow(t *testing.T, workflow *Workflow) {
	require.NotNil(t, workflow)

	require.Equal(t, "up", workflow.Name)
	require.Len(t, workflow.Steps, 3)
	require.Len(t, workflow.Steps[0].AzdCommand.Args, 2)
	require.Equal(t, "package", workflow.Steps[0].AzdCommand.Args[0])
	require.Equal(t, "--all", workflow.Steps[0].AzdCommand.Args[1])
}
