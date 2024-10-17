package contracts

import "github.com/azure/azure-dev/cli/sdk/azdcore/common"

type KustomizeConfig struct {
	Directory common.ExpandableString            `yaml:"dir"`
	Edits     []common.ExpandableString          `yaml:"edits"`
	Env       map[string]common.ExpandableString `yaml:"env"`
}
