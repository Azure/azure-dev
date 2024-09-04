package workflow

import (
	"fmt"
	"strings"

	"github.com/braydonk/yaml"
)

// Workflow stores a list of steps to execute
type Workflow struct {
	Name  string  `yaml:"-"`
	Steps []*Step `yaml:"steps,omitempty"`
}

// UnmarshalYAML will unmarshal the Workflow from YAML.
// The workflow YAML can be specified as either a simple array of steps or a more verbose map/struct style
func (w *Workflow) UnmarshalYAML(unmarshal func(interface{}) error) error {
	parsed := false

	// Map
	var m map[string]interface{}
	if err := unmarshal(&m); err == nil {
		rawName, has := m["name"]
		if has {
			w.Name = rawName.(string)
		}

		rawSteps, has := m["steps"]
		if has {
			stepsArray, ok := rawSteps.([]interface{})
			if ok {
				w.Steps, err = w.unmarshalSteps(stepsArray)
				if err != nil {
					return err
				}
			}
		}

		parsed = true
	}

	// Array
	var steps []interface{}
	if err := unmarshal(&steps); err == nil {
		w.Steps, err = w.unmarshalSteps(steps)
		if err != nil {
			return err
		}

		parsed = true
	}

	if !parsed || len(w.Steps) == 0 {
		return fmt.Errorf("workflow configuration must be a map or an array of steps")
	}

	return nil
}

// unmarshalSteps will unmarshal the steps from YAML.
func (w *Workflow) unmarshalSteps(rawSteps any) ([]*Step, error) {
	stepsArray, ok := rawSteps.([]interface{})
	if !ok {
		return nil, fmt.Errorf("steps must be an array")
	}

	steps := []*Step{}

	for _, rawStep := range stepsArray {
		stepYaml, err := yaml.Marshal(rawStep)
		if err != nil {
			return nil, err
		}

		var step Step
		if err := yaml.Unmarshal(stepYaml, &step); err != nil {
			return nil, err
		}

		steps = append(steps, &step)
	}

	return steps, nil
}

// Step stores a single step to execute within a workflow
// This struct can be expanded over time to support other types of steps/commands
type Step struct {
	AzdCommand Command `yaml:"azd,omitempty"`
}

// NewAzdCommandStep creates a new step that executes an azd command with the specified name and args
func NewAzdCommandStep(args ...string) *Step {
	return &Step{
		AzdCommand: Command{
			Args: args,
		},
	}
}

// Command stores a single command to execute
type Command struct {
	Args []string `yaml:"args,omitempty"`
}

// UnmarshalYAML will unmarshal the Command from YAML.
// In command YAML the command can be specified as a simple string or a more verbose map/struct style
func (c *Command) UnmarshalYAML(unmarshal func(interface{}) error) error {
	parsed := false

	// Map
	var m map[string]interface{}
	if err := unmarshal(&m); err == nil {
		rawArgs, has := m["args"]
		if has {
			argsArray, ok := rawArgs.([]interface{})
			if ok {
				for _, arg := range argsArray {
					argValue, ok := arg.(string)
					if ok {
						c.Args = append(c.Args, argValue)
					}
				}
			}
		}

		parsed = true
	}

	// String
	var s string
	if err := unmarshal(&s); err == nil {
		parts := strings.Split(s, " ")
		if len(parts) > 0 {
			c.Args = parts
		}

		parsed = true
	}

	if !parsed {
		return fmt.Errorf("command must be a string or a map")
	}

	return nil
}
