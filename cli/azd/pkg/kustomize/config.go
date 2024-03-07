package kustomize

import "github.com/azure/azure-dev/cli/azd/pkg/osutil"

type Config struct {
	Directory osutil.ExpandableString            `yaml:"dir"`
	Edits     []osutil.ExpandableString          `yaml:"edits"`
	Env       map[string]osutil.ExpandableString `yaml:"env"`
}
