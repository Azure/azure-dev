package workflow

import (
	"strings"

	"gopkg.in/yaml.v3"
)

type WorkflowMap struct {
	inner map[string]*Workflow
}

func NewWorkflowMap() *WorkflowMap {
	return &WorkflowMap{
		inner: map[string]*Workflow{},
	}
}

// Set adds or updates a key-value pair in the map.
func (wm *WorkflowMap) Set(key string, value *Workflow) {
	if wm.inner == nil {
		wm.inner = map[string]*Workflow{}
	}

	wm.inner[key] = value
}

// Get retrieves the value for a given key from the map.
func (wm *WorkflowMap) Get(key string) (*Workflow, bool) {
	if wm.inner == nil {
		wm.inner = map[string]*Workflow{}
	}

	val, ok := wm.inner[key]
	return val, ok
}

func (wm *WorkflowMap) MarshalYAML() (interface{}, error) {
	return wm.inner, nil
}

func (wm *WorkflowMap) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var m map[string]*Workflow
	if err := unmarshal(&m); err != nil {
		return err
	}

	for key, workflow := range m {
		workflow.Name = key
	}

	wm.inner = m
	return nil
}

type AzdCommand Command

func (command *AzdCommand) MarshalYAML() (interface{}, error) {
	c := Command{
		Name: "",
		Args: command.Args,
	}

	return yaml.Marshal(c)
}

func (command *AzdCommand) UnmarshalYAML(unmarshal func(interface{}) error) error {
	command.Name = "azd"

	var args []string
	if err := unmarshal(&args); err != nil {
		command.Args = args
		return nil
	}

	var cmdString string
	if err := unmarshal(&cmdString); err != nil {
		command.Args = strings.Split(cmdString, " ")
	}

	return nil
}
