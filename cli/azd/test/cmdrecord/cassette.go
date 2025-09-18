// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmdrecord

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/braydonk/yaml"
)

const InteractionIdFile = "int-id.txt"

type Cassette struct {
	Version  string `yaml:"version"`
	ToolName string `yaml:"tool"`

	Interactions []Interaction `yaml:"interactions"`
}

type Interaction struct {
	Id       int      `yaml:"id"`
	Args     []string `yaml:"args"`
	ExitCode int      `yaml:"exitCode"`
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
		err = os.WriteFile(expandedName(dir, c.ToolName, i.Id, ".out"), []byte(i.Stdout), 0600)
		if err != nil {
			return err
		}

		err = os.WriteFile(expandedName(dir, c.ToolName, i.Id, ".err"), []byte(i.Stderr), 0600)
		if err != nil {
			return err
		}

		i.Stdout = ""
		i.Stderr = ""
		metadata, err := yaml.Marshal(i)
		if err != nil {
			return err
		}

		err = os.WriteFile(expandedName(dir, c.ToolName, i.Id, ".meta"), metadata, 0600)
		if err != nil {
			return err
		}
	}

	return nil
}

func zip(cassette string, tool string, dir string) error {
	cst := Cassette{Version: "1.0", ToolName: tool}
	maxIntIdContent, err := os.ReadFile(filepath.Join(dir, InteractionIdFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}

	maxId, err := strconv.Atoi(string(maxIntIdContent))
	if err != nil {
		return err
	}

	for id := 0; id <= maxId; id++ {
		interaction := Interaction{Id: id}
		meta, err := os.ReadFile(expandedName(dir, tool, id, ".meta"))
		if err != nil {
			return err
		}

		err = yaml.Unmarshal(meta, &interaction)
		if err != nil {
			return fmt.Errorf("reading .meta: %w", err)
		}

		interaction.Id = id
		stdout, err := os.ReadFile(expandedName(dir, tool, id, ".out"))
		if err != nil {
			return err
		}

		interaction.Stdout = string(stdout)

		stderr, err := os.ReadFile(expandedName(dir, tool, id, ".err"))
		if err != nil {
			return err
		}

		interaction.Stderr = string(stderr)
		cst.Interactions = append(cst.Interactions, interaction)
	}

	if len(cst.Interactions) == 0 { // avoid saving empty cassettes
		return nil
	}

	return save(cst, cassette)
}

func save(cst Cassette, cassette string) error {
	cstYml, err := yaml.Marshal(cst)
	if err != nil {
		return err
	}

	return os.WriteFile(cassette, cstYml, 0600)
}

func expandedName(root string, tool string, interaction int, ext string) string {
	return filepath.Join(root, fmt.Sprintf("%s.%d%s", tool, interaction, ext))
}
