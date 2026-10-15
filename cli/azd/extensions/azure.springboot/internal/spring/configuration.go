// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/magiconair/properties"
	"go.yaml.in/yaml/v3"
)

// LookupEnv provides explicit Spring activation and property overrides.
type LookupEnv func(string) (string, bool)

type property struct {
	text   string
	source string
}

type configuration struct {
	values        map[string]property
	profiles      []string
	lookup        LookupEnv
	externalFiles []string
}

type document struct {
	values map[string]property
	file   string
}

var placeholder = regexp.MustCompile(`\$\{([^{}]+)\}`)
var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var configurationLocations = [][]string{
	{"src/main/resources", "src/main/resources/config"},
	{".", "config"},
}

func canonical(key string) string {
	return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
}

func readConfiguration(ctx context.Context, root *os.Root, lookup LookupEnv) (*configuration, error) {
	c := &configuration{values: map[string]property{}, lookup: lookup}
	locations := [][]string{configurationLocations[0], slices.Clone(configurationLocations[1])}
	entries, err := fs.ReadDir(root.FS(), "config")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("reading external configuration directories: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			locations[1] = append(locations[1], path.Join("config", entry.Name()))
		}
	}
	var allLocations []string
	for _, group := range locations {
		allLocations = append(allLocations, group...)
	}
	base, err := readDocuments(ctx, root, "", allLocations)
	if err != nil {
		return nil, err
	}
	for _, doc := range base {
		if _, cloud := doc.values[canonical("spring.config.activate.on-cloud-platform")]; cloud {
			continue
		}
		if _, activated := doc.values[canonical("spring.config.activate.on-profile")]; !activated {
			maps.Copy(c.values, doc.values)
		}
	}
	var jsonProperties map[string]property
	if text, ok := lookup("SPRING_APPLICATION_JSON"); ok && text != "" {
		var values map[string]any
		if err := json.Unmarshal([]byte(text), &values); err != nil {
			return nil, errors.New("SPRING_APPLICATION_JSON must contain a valid JSON object")
		}
		flat := map[string]property{}
		if err := flatten(values, "", "SPRING_APPLICATION_JSON", flat); err != nil {
			return nil, err
		}
		// JSON properties have higher precedence than ordinary environment variables.
		previous := c.lookup
		c.lookup = func(key string) (string, bool) {
			if p, ok := flat[canonical(key)]; ok {
				return p.text, true
			}
			return previous(key)
		}
		maps.Copy(c.values, flat)
		jsonProperties = flat
	}
	active, err := c.resolve("spring.profiles.active")
	if err != nil {
		return nil, err
	}
	included, err := c.resolve("spring.profiles.include")
	if err != nil {
		return nil, err
	}
	names := splitList(included + "," + active)
	if len(names) == 0 {
		defaults, err := c.resolve("spring.profiles.default")
		if err != nil {
			return nil, err
		}
		names = splitList(defaults)
		if len(names) == 0 {
			names = []string{"default"}
		} else if slices.Equal(names, []string{"none"}) {
			names = nil
		}
	}
	for i := 0; i < len(names); i++ {
		if len(names) > 64 || !profileName.MatchString(names[i]) {
			return nil, errors.New("Spring profile activation is invalid or exceeds 64 profiles")
		}
		group, err := c.resolve("spring.profiles.group." + names[i])
		if err != nil {
			return nil, err
		}
		for _, member := range splitList(group) {
			if !slices.Contains(names, member) {
				names = append(names, member)
			}
		}
	}
	c.profiles = names
	selected := make(map[string]bool, len(names))
	for _, name := range names {
		selected[name] = true
	}
	c.values = map[string]property{}
	// External base configuration outranks packaged profile-specific configuration.
	for _, locations := range locations {
		docs, err := readDocuments(ctx, root, "", locations)
		if err != nil {
			return nil, err
		}
		for _, doc := range docs {
			if err := c.apply(doc, selected, false); err != nil {
				return nil, err
			}
		}
		for _, name := range names {
			docs, err := readDocuments(ctx, root, name, locations)
			if err != nil {
				return nil, err
			}
			for _, doc := range docs {
				if err := c.apply(doc, selected, true); err != nil {
					return nil, err
				}
			}
		}
	}
	maps.Copy(c.values, jsonProperties)
	for key := range c.values {
		if strings.Contains(key, "[") && strings.HasPrefix(key, "spring.profiles.") {
			return nil, errors.New("indexed profile properties require list-binding analysis not yet supported")
		}
	}
	for _, key := range []string{"spring.config.import", "spring.config.location",
		"spring.config.additional-location", "spring.config.name"} {
		value, err := c.resolve(key)
		if err != nil {
			return nil, err
		}
		if value != "" {
			return nil, fmt.Errorf("%s requires configuration-location analysis not yet supported", key)
		}
	}
	return c, nil
}

func (c *configuration) apply(doc document, selected map[string]bool, profileFile bool) error {
	condition, conditional := doc.values[canonical("spring.config.activate.on-profile")]
	if conditional {
		match, err := matchesProfile(condition.text, selected)
		if err != nil {
			return fmt.Errorf("invalid profile activation in %s", condition.source)
		}
		if !match {
			return nil
		}
	}
	if _, cloud := doc.values[canonical("spring.config.activate.on-cloud-platform")]; cloud {
		return errors.New("cloud-platform configuration activation cannot yet be inferred")
	}
	if conditional || profileFile {
		for _, key := range []string{"spring.profiles.active", "spring.profiles.include", "spring.profiles.default"} {
			if _, ok := doc.values[canonical(key)]; ok {
				return fmt.Errorf("%s cannot be declared in profile-specific configuration", key)
			}
			for key := range doc.values {
				if strings.HasPrefix(key, "spring.profiles.group.") {
					return errors.New("spring.profiles.group cannot be declared in profile-specific configuration")
				}
			}
		}
	}
	maps.Copy(c.values, doc.values)
	if !strings.HasPrefix(doc.file, "src/main/resources/") && !slices.Contains(c.externalFiles, doc.file) {
		c.externalFiles = append(c.externalFiles, doc.file)
	}
	return nil
}

func readDocuments(ctx context.Context, root *os.Root, profile string, locations []string) ([]document, error) {
	name := "application"
	if profile != "" {
		name += "-" + profile
	}
	var docs []document
	for _, location := range locations {
		for _, extension := range []string{".yaml", ".yml", ".properties"} {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			file := path.Join(location, name+extension)
			data, err := root.ReadFile(file)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("reading configuration %s: %w", file, err)
			}
			if len(data) > 1024*1024 {
				return nil, fmt.Errorf("configuration %s exceeds 1 MiB", file)
			}
			if extension == ".properties" {
				for _, part := range splitPropertiesDocuments(string(data)) {
					p := properties.NewProperties()
					p.DisableExpansion = true
					if err := p.Load([]byte(part), properties.UTF8); err != nil {
						return nil, fmt.Errorf("invalid Java properties in %s", file)
					}
					values := map[string]property{}
					for key, value := range p.Map() {
						values[canonical(key)] = property{text: value, source: file}
					}
					docs = append(docs, document{values: values, file: file})
				}
				continue
			}

			decoder := yaml.NewDecoder(strings.NewReader(string(data)))
			for {
				var values map[string]any
				err := decoder.Decode(&values)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return nil, fmt.Errorf("invalid YAML configuration in %s", file)
				}
				flat := map[string]property{}
				if err := flatten(values, "", file, flat); err != nil {
					return nil, err
				}
				docs = append(docs, document{values: flat, file: file})
			}
		}
	}
	return docs, nil
}

func splitPropertiesDocuments(data string) []string {
	var result []string
	var current strings.Builder
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "#---" || line == "!---" {
			result = append(result, current.String())
			current.Reset()
		} else {
			current.WriteString(line)
			current.WriteByte('\n')
		}
	}
	return append(result, current.String())
}

func flatten(values map[string]any, prefix, source string, result map[string]property) error {
	for key, value := range values {
		name := key
		if prefix != "" {
			name = prefix + "." + key
		}
		switch value := value.(type) {
		case map[string]any:
			if err := flatten(value, name, source, result); err != nil {
				return err
			}
		case []any:
			items := make([]string, len(value))
			for i, item := range value {
				switch item := item.(type) {
				case string:
					items[i] = item
				default:
					return fmt.Errorf("non-scalar configuration list in %s", source)
				}
			}
			result[canonical(name)] = property{text: strings.Join(items, ","), source: source}
		case nil:
			if source == "SPRING_APPLICATION_JSON" {
				continue
			}
			result[canonical(name)] = property{source: source}
		case string, bool, int, int64, float64:
			result[canonical(name)] = property{text: fmt.Sprint(value), source: source}
		default:
			return fmt.Errorf("unsupported configuration value in %s", source)
		}
	}
	return nil
}

func (c *configuration) raw(key string) (property, bool) {
	for _, name := range []string{key,
		strings.ToUpper(strings.NewReplacer(".", "_", "-", "").Replace(key)),
		strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))} {
		if text, ok := c.lookup(name); ok {
			return property{text: text, source: "environment"}, true
		}
	}
	p, ok := c.values[canonical(key)]
	return p, ok
}

func (c *configuration) resolve(key string) (string, error) {
	return c.expand(key, map[string]bool{})
}

func (c *configuration) expand(key string, visiting map[string]bool) (string, error) {
	if visiting[key] || len(visiting) >= 32 {
		return "", errors.New("Spring property substitution is circular or exceeds 32 levels")
	}
	visiting[key] = true
	defer delete(visiting, key)
	p, ok := c.raw(key)
	if !ok {
		return "", nil
	}
	text := p.text
	for depth := 0; depth < 32 && strings.Contains(text, "${"); depth++ {
		matches := placeholder.FindAllStringSubmatchIndex(text, -1)
		if len(matches) == 0 {
			return "", errors.New("Spring configuration has a malformed property placeholder")
		}
		for i := len(matches) - 1; i >= 0; i-- {
			match := matches[i]
			inner := text[match[2]:match[3]]
			name, fallback, hasDefault := strings.Cut(inner, ":")
			value := fallback
			if _, exists := c.raw(name); exists {
				var err error
				value, err = c.expand(name, visiting)
				if err != nil {
					return "", err
				}
			} else if !hasDefault {
				return "", errors.New("a required Spring configuration placeholder is unresolved")
			}
			text = text[:match[0]] + value + text[match[1]:]
		}
	}
	if strings.Contains(text, "${") {
		return "", errors.New("Spring substitution exceeds 32 levels")
	}
	return text, nil
}

func splitList(text string) []string {
	var result []string
	for name := range strings.SplitSeq(text, ",") {
		name = strings.TrimSpace(name)
		if name != "" && !slices.Contains(result, name) {
			result = append(result, name)
		}
	}
	return result
}

func (c *configuration) integer(key string, fallback int) (int, error) {
	text, err := c.resolve(key)
	if err != nil {
		return 0, err
	}
	if text == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}
	return value, nil
}
