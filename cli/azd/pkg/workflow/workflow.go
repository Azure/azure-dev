package workflow

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type Workflow struct {
	Name  string  `yaml:"-"`
	Steps []*Step `yaml:"steps,omitempty"`
}

func (w *Workflow) UnmarshalYAML(unmarshal func(interface{}) error) error {
	valid := false

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
				w.Steps, err = w.UnmarshallSteps(stepsArray)
				if err != nil {
					return err
				}
			}
		}

		valid = true
	}

	// Array
	var steps []interface{}
	if err := unmarshal(&steps); err == nil {
		w.Steps, err = w.UnmarshallSteps(steps)
		if err != nil {
			return err
		}

		valid = true
	}

	if !valid || len(w.Steps) == 0 {
		return fmt.Errorf("workflow configuration must be a map or an array of steps")
	}

	return nil
}

func (w *Workflow) UnmarshallSteps(rawSteps any) ([]*Step, error) {
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

type Step struct {
	AzdCommand Command `yaml:"azd,omitempty"`
}

type Command struct {
	Name string   `yaml:"command,omitempty"`
	Args []string `yaml:"args,omitempty"`
}

func (c *Command) UnmarshalYAML(unmarshal func(interface{}) error) error {
	parsed := false

	// Map
	var m map[string]interface{}
	if err := unmarshal(&m); err == nil {
		rawName, has := m["command"]
		if has {
			c.Name = rawName.(string)
		}

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
			c.Name = parts[0]
		}
		if len(parts) > 1 {
			c.Args = parts[1:]
		}

		parsed = true
	}

	if !parsed {
		return fmt.Errorf("command must be a string or a map")
	}

	return nil
}
