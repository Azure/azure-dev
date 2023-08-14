package cmdrecord

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

var expandedFileNameRegex = regexp.MustCompile(`(?P<tool>[^\.]+)\.(?P<interaction>\d+)\.(?P<ext>\w+)`)

type Cassette struct {
	Version  string `yaml:"version"`
	ToolName string `yaml:"tool"`

	Interactions []Interaction `yaml:"interactions"`
}

type Interaction struct {
	Id       int      `yaml:"id"`
	Args     []string `yaml:"args"`
	ExitCode []string `yaml:"exitCode"`
	Stdout   string   `yaml:"stdout"`
	Stderr   string   `yaml:"stderr"`
}

func expand(cassette string, dir string) error {
	contents, err := os.ReadFile(cassette)
	if err != nil {
		return fmt.Errorf("reading cassette '%s': %w", cassette, err)
	}

	var c Cassette
	err = yaml.Unmarshal(contents, &c)
	if err != nil {
		return fmt.Errorf("unmarshalling cassette '%s': %w", cassette, err)
	}

	err = os.MkdirAll(dir, 0755)
	if err != nil {
		return fmt.Errorf("creating dir '%s': %w", dir, err)
	}

	for _, i := range c.Interactions {
		err = os.WriteFile(expandedName(dir, c.ToolName, i.Id, ".out"), []byte(i.Stdout), 0644)
		if err != nil {
			return err
		}

		err = os.WriteFile(expandedName(dir, c.ToolName, i.Id, ".err"), []byte(i.Stderr), 0644)
		if err != nil {
			return err
		}

		i.Stdout = ""
		i.Stderr = ""
		metadata, err := yaml.Marshal(i)
		if err != nil {
			return err
		}

		err = os.WriteFile(expandedName(dir, c.ToolName, i.Id, ".meta"), metadata, 0644)
		if err != nil {
			return err
		}
	}

	return nil
}

func zip(cassette string, tool string, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}

		if ent.Name() == "meta" {
			continue
		}

		contents, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			return err
		}

		err = os.WriteFile(expandedName(cassette, tool, 0, ".out"), contents, 0644)
		if err != nil {
			return err
		}
	}

	return nil
}

func expandedName(root string, tool string, interaction int, ext string) string {
	return filepath.Join(root, fmt.Sprintf("%s.%d%s", tool, interaction, ext))
}
