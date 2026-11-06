// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"slices"
	"strings"
)

func profileChoices(root *os.Root, locations []string, base []document) ([]string, error) {
	names := map[string]bool{}
	add := func(name string) {
		if profileName.MatchString(name) && name != "default" && name != "none" {
			names[name] = true
		}
	}
	for _, location := range locations {
		entries, err := fs.ReadDir(root.FS(), location)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("discovering profile choices: %w", err)
		}
		for _, entry := range entries {
			extension := path.Ext(entry.Name())
			if !entry.IsDir() && slices.Contains([]string{".properties", ".yaml", ".yml"}, extension) &&
				strings.HasPrefix(entry.Name(), "application-") {
				add(strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "application-"), extension))
			}
		}
	}
	for _, doc := range base {
		if condition, ok := doc.values[canonical("spring.config.activate.on-profile")]; ok {
			// Complex expressions are not independently selectable profile names.
			for _, name := range splitList(condition.text) {
				add(name)
			}
		}
		for name := range doc.profileGroups {
			add(name)
		}
	}
	return slices.Sorted(maps.Keys(names)), nil
}
