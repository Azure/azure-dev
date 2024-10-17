package contracts

import "github.com/azure/azure-dev/cli/sdk/azdcore/common"

type DockerProjectOptions struct {
	Path        string                    `yaml:"path,omitempty"        json:"path,omitempty"`
	Context     string                    `yaml:"context,omitempty"     json:"context,omitempty"`
	Platform    string                    `yaml:"platform,omitempty"    json:"platform,omitempty"`
	Target      string                    `yaml:"target,omitempty"      json:"target,omitempty"`
	Registry    common.ExpandableString   `yaml:"registry,omitempty"    json:"registry,omitempty"`
	Image       common.ExpandableString   `yaml:"image,omitempty"       json:"image,omitempty"`
	Tag         common.ExpandableString   `yaml:"tag,omitempty"         json:"tag,omitempty"`
	RemoteBuild bool                      `yaml:"remoteBuild,omitempty" json:"remoteBuild,omitempty"`
	BuildArgs   []common.ExpandableString `yaml:"buildArgs,omitempty"   json:"buildArgs,omitempty"`
	// not supported from azure.yaml directly yet. Adding it for Aspire to use it, initially.
	// Aspire would pass the secret keys, which are env vars that azd will set just to run docker build.
	BuildSecrets []string `yaml:"-"                     json:"-"`
	BuildEnv     []string `yaml:"-"                     json:"-"`
}
