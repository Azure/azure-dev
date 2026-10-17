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
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/magiconair/properties"
	"go.yaml.in/yaml/v3"
)

// LookupEnv supplies property overrides from a caller-selected source.
type LookupEnv func(string) (string, bool)

type property struct {
	text   string
	source string
	key    string
}

type configuration struct {
	values        map[string]property
	profileGroups map[string]property
	profiles      []string
	lookup        LookupEnv
	externalFiles []string
	traces        map[string][]Evidence
	trace         []Evidence
}

type document struct {
	values        map[string]property
	profileGroups map[string]property
	file          string
}

var placeholder = regexp.MustCompile(`\$\{([^{}]+)\}`)
var profileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
var propertyName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
var configurationLocations = [][]string{
	{"src/main/resources", "src/main/resources/config"},
	{".", "config"},
}

func canonical(key string) string {
	return strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
}

func profileGroupName(key string) (string, bool) {
	parts := strings.Split(key, ".")
	if len(parts) < 4 || canonical(strings.Join(parts[:3], ".")) != canonical("spring.profiles.group") {
		return "", false
	}
	return strings.Join(parts[3:], "."), true
}

func storageKey(key string) string {
	if name, ok := profileGroupName(key); ok {
		return "spring.profiles.group." + name
	}
	return canonical(key)
}

func newDocument(values map[string]property, file string) document {
	groups := map[string]property{}
	for key, value := range values {
		if name, ok := profileGroupName(value.key); ok {
			groups[name] = value
			delete(values, key)
		}
	}
	return document{values: values, profileGroups: groups, file: file}
}

func readConfiguration(ctx context.Context, root *os.Root, lookup LookupEnv) (*configuration, error) {
	c := &configuration{values: map[string]property{}, profileGroups: map[string]property{}, lookup: lookup}
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
	if err := validateKnownCredentials(ctx, root, configurationLocations[0]); err != nil {
		return nil, err
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
			maps.Copy(c.profileGroups, doc.profileGroups)
		}
	}
	var jsonProperties map[string]property
	if text, ok := lookup("SPRING_APPLICATION_JSON"); ok && text != "" {
		var values map[string]any
		if err := json.Unmarshal([]byte(text), &values); err != nil {
			return nil, invalidConfiguration("SPRING_APPLICATION_JSON must contain a valid JSON object")
		}
		flat := map[string]property{}
		if err := flatten(values, "", "SPRING_APPLICATION_JSON", flat); err != nil {
			return nil, err
		}
		jsonDocument := newDocument(flat, "SPRING_APPLICATION_JSON")
		flat = jsonDocument.values
		maps.Copy(c.profileGroups, jsonDocument.profileGroups)
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
		if missing, ok := errors.AsType[*missingInput](err); ok {
			missing.choices, err = profileChoices(root, allLocations, base)
			if err != nil {
				return nil, err
			}
			return nil, missing
		}
		return nil, err
	}
	includeKey := "spring.profiles.include"
	var includeSources []property
	if p, ok := jsonProperties[canonical(includeKey)]; ok {
		includeSources = append(includeSources, p)
	}
	if p, ok := lookupEnvProperty(lookup, includeKey); ok {
		includeSources = append(includeSources, p)
	}
	for _, doc := range slices.Backward(base) {
		if _, cloud := doc.values[canonical("spring.config.activate.on-cloud-platform")]; cloud {
			continue
		}
		if _, activated := doc.values[canonical("spring.config.activate.on-profile")]; activated {
			continue
		}
		if p, ok := doc.values[canonical(includeKey)]; ok {
			includeSources = append(includeSources, p)
		}
	}
	var included strings.Builder
	for _, p := range includeSources {
		text, err := c.expandProperty(includeKey, p, map[string]bool{})
		if err != nil {
			return nil, fmt.Errorf("resolving profile includes in %s: %w", p.source, err)
		}
		included.WriteString(text)
		included.WriteByte(',')
	}
	names := splitList(included.String() + active)
	if len(names) == 0 {
		defaults, err := c.resolve("spring.profiles.default")
		if err != nil {
			if missing, ok := errors.AsType[*missingInput](err); ok {
				missing.choices, err = profileChoices(root, allLocations, base)
				if err != nil {
					return nil, err
				}
				return nil, missing
			}
			return nil, err
		}
		names = splitList(defaults)
		if len(names) == 0 {
			names = []string{"default"}
		} else if slices.Equal(names, []string{"none"}) {
			names = nil
		}
	}
	pending := names
	names = nil
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		if slices.Contains(names, name) {
			continue
		}
		if len(names) >= 64 || !profileName.MatchString(name) {
			return nil, invalidConfiguration("Spring profile activation is invalid or exceeds 64 profiles")
		}
		names = append(names, name)
		group, err := c.resolveProfileGroup(name)
		if err != nil {
			return nil, err
		}
		pending = append(splitList(group), pending...)
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
			return nil, unsupportedConfiguration(
				"indexed profile properties require list-binding analysis not yet supported")
		}
	}
	for _, key := range []string{"spring.config.import", "spring.config.location",
		"spring.config.additional-location", "spring.config.name"} {
		value, err := c.resolve(key)
		if err != nil {
			return nil, err
		}
		if value != "" {
			return nil, unsupportedConfiguration("%s requires configuration-location analysis not yet supported", key)
		}
	}
	if err := validateCredentialFiles(ctx, root, c.externalFiles); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *configuration) apply(doc document, selected map[string]bool, profileFile bool) error {
	condition, conditional := doc.values[canonical("spring.config.activate.on-profile")]
	if conditional {
		match, err := matchesProfile(condition.text, selected)
		if err != nil {
			return invalidConfiguration("invalid profile activation in %s", condition.source)
		}
		if !match {
			return nil
		}
	}
	if _, cloud := doc.values[canonical("spring.config.activate.on-cloud-platform")]; cloud {
		return unsupportedConfiguration("cloud-platform configuration activation cannot yet be inferred")
	}
	if conditional || profileFile {
		for _, key := range []string{"spring.profiles.active", "spring.profiles.include", "spring.profiles.default"} {
			if _, ok := doc.values[canonical(key)]; ok {
				return invalidConfiguration("%s cannot be declared in profile-specific configuration", key)
			}
			if len(doc.profileGroups) != 0 {
				return invalidConfiguration("spring.profiles.group cannot be declared in profile-specific configuration")
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
			fileDocs, err := readDocumentFile(root, file, extension)
			if err != nil {
				return nil, err
			}
			docs = append(docs, fileDocs...)
		}
	}
	return docs, nil
}

func readDocumentFile(root *os.Root, file, extension string) ([]document, error) {
	data, err := root.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading configuration %s: %w", file, err)
	}
	if len(data) > 1024*1024 {
		return nil, invalidConfiguration("configuration %s exceeds 1 MiB", file)
	}
	if extension == ".properties" {
		var docs []document
		for _, part := range splitPropertiesDocuments(string(data)) {
			p := properties.NewProperties()
			p.DisableExpansion = true
			if err := p.Load([]byte(part), properties.UTF8); err != nil {
				return nil, invalidConfiguration("invalid Java properties in %s", file)
			}
			values := map[string]property{}
			for key, value := range p.Map() {
				values[storageKey(key)] = property{text: value, source: file, key: key}
			}
			docs = append(docs, newDocument(values, file))
		}
		return docs, nil
	}

	var docs []document
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	for {
		var values map[string]any
		err := decoder.Decode(&values)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, invalidConfiguration("invalid YAML configuration in %s", file)
		}
		flat := map[string]property{}
		if err := flatten(values, "", file, flat); err != nil {
			return nil, err
		}
		docs = append(docs, newDocument(flat, file))
	}
	return docs, nil
}

func validateKnownCredentials(ctx context.Context, root *os.Root, locations []string) error {
	seen := map[string]bool{}
	for _, location := range locations {
		entries, err := fs.ReadDir(root.FS(), location)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading configuration location %s: %w", location, err)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			extension, ok := applicationConfigurationExtension(entry.Name())
			if entry.IsDir() || !ok {
				continue
			}
			file := path.Join(location, entry.Name())
			if seen[file] {
				continue
			}
			seen[file] = true
			docs, err := readDocumentFile(root, file, extension)
			if err != nil {
				return err
			}
			for _, doc := range docs {
				if err := validateKnownCredentialDocument(doc); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateCredentialFiles(ctx context.Context, root *os.Root, files []string) error {
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		extension := path.Ext(file)
		docs, err := readDocumentFile(root, file, extension)
		if err != nil {
			return err
		}
		for _, doc := range docs {
			if err := validateKnownCredentialDocument(doc); err != nil {
				return err
			}
		}
	}
	return nil
}

func applicationConfigurationExtension(name string) (string, bool) {
	for _, extension := range []string{".yaml", ".yml", ".properties"} {
		base, ok := strings.CutSuffix(name, extension)
		if ok && (base == "application" || strings.HasPrefix(base, "application-")) {
			return extension, true
		}
	}
	return "", false
}

func validateKnownCredentialDocument(doc document) error {
	if password, ok := doc.values[canonical("spring.datasource.password")]; ok &&
		hasLiteralCredential(password.text) {
		return unsupportedConfiguration(
			"literal datasource credentials in %s are not supported; "+
				"replace %s with a runtime placeholder without a default",
			password.source, password.key)
	}
	if jdbc, ok := doc.values[canonical("spring.datasource.url")]; ok &&
		jdbcURLHasLiteralPassword(jdbc.text) {
		return unsupportedConfiguration(
			"literal JDBC credentials in %s are not supported; "+
				"replace the password in %s with a runtime placeholder without a default",
			jdbc.source, jdbc.key)
	}
	return nil
}

func hasLiteralCredential(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	_, fallback, placeholderOnly := runtimePlaceholder(value)
	return !placeholderOnly || fallback != ""
}

func runtimePlaceholder(value string) (name, fallback string, ok bool) {
	match := placeholder.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) != 2 || match[0] != strings.TrimSpace(value) {
		return "", "", false
	}
	name, fallback, found := strings.Cut(match[1], ":")
	if !propertyName.MatchString(name) {
		return "", "", false
	}
	if !found {
		fallback = ""
	}
	return name, fallback, true
}

func jdbcURLHasLiteralPassword(value string) bool {
	value = strings.TrimSpace(value)
	if _, fallback, ok := runtimePlaceholder(value); ok {
		if fallback == "" {
			return false
		}
		value = fallback
	}
	if !strings.HasPrefix(value, "jdbc:mysql://") && !strings.HasPrefix(value, "jdbc:postgresql://") {
		return false
	}
	address, err := url.Parse(strings.TrimPrefix(value, "jdbc:"))
	if err != nil {
		return false
	}
	if address.User != nil {
		if password, present := address.User.Password(); present && hasLiteralCredential(password) {
			return true
		}
	}
	query, err := url.ParseQuery(address.RawQuery)
	if err != nil {
		return false
	}
	for key, values := range query {
		if !strings.EqualFold(key, "password") {
			continue
		}
		if slices.ContainsFunc(values, hasLiteralCredential) {
			return true
		}
	}
	return false
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
				case string, bool, int, int64, float64:
					items[i] = fmt.Sprint(item)
				default:
					return unsupportedConfiguration("non-scalar configuration list in %s", source)
				}
			}
			result[storageKey(name)] = property{text: strings.Join(items, ","), source: source, key: name}
		case nil:
			if source == "SPRING_APPLICATION_JSON" {
				continue
			}
			result[storageKey(name)] = property{source: source, key: name}
		case string, bool, int, int64, float64:
			result[storageKey(name)] = property{text: fmt.Sprint(value), source: source, key: name}
		default:
			return unsupportedConfiguration("unsupported configuration value in %s", source)
		}
	}
	return nil
}

func lookupEnvProperty(lookup LookupEnv, key string) (property, bool) {
	for _, name := range []string{key,
		strings.ToUpper(strings.NewReplacer(".", "_", "-", "").Replace(key)),
		strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(key))} {
		if text, ok := lookup(name); ok {
			return property{text: text, source: "environment"}, true
		}
	}
	return property{}, false
}

func (c *configuration) raw(key string) (property, bool) {
	if p, ok := lookupEnvProperty(c.lookup, key); ok {
		return p, true
	}
	p, ok := c.values[canonical(key)]
	return p, ok
}

func (c *configuration) resolve(key string) (string, error) {
	c.trace = nil
	value, err := c.expand(key, map[string]bool{})
	if c.traces == nil {
		c.traces = map[string][]Evidence{}
	}
	c.traces[key] = slices.Clone(c.trace)
	return value, err
}

func (c *configuration) resolveProfileGroup(name string) (string, error) {
	key := "spring.profiles.group." + name
	if p, ok := c.profileGroups[name]; ok && p.source == "SPRING_APPLICATION_JSON" {
		return c.expandProperty(key, p, map[string]bool{})
	}
	if p, ok := lookupEnvProperty(c.lookup, key); ok {
		return c.expandProperty(key, p, map[string]bool{})
	}
	if p, ok := c.profileGroups[name]; ok {
		return c.expandProperty(key, p, map[string]bool{})
	}
	return "", nil
}

func (c *configuration) expand(key string, visiting map[string]bool) (string, error) {
	if visiting[key] || len(visiting) >= 32 {
		return "", invalidConfiguration("Spring property substitution is circular or exceeds 32 levels")
	}
	visiting[key] = true
	defer delete(visiting, key)
	p, ok := c.raw(key)
	if !ok {
		return "", nil
	}
	return c.expandProperty(key, p, visiting)
}

func (c *configuration) expandProperty(key string, p property, visiting map[string]bool) (string, error) {
	evidence := Evidence{Property: key, Source: p.source}
	if !slices.Contains(c.trace, evidence) {
		c.trace = append(c.trace, evidence)
	}
	value, err := c.expandText(p.text, visiting)
	if missing, ok := errors.AsType[*missingInput](err); ok {
		missing.evidence = append([]Evidence{evidence}, missing.evidence...)
	}
	return value, err
}

func (c *configuration) expandText(text string, visiting map[string]bool) (string, error) {
	for depth := 0; depth < 32 && strings.Contains(text, "${"); depth++ {
		matches := placeholder.FindAllStringSubmatchIndex(text, -1)
		if len(matches) == 0 {
			return "", invalidConfiguration("Spring configuration has a malformed property placeholder")
		}
		for i := len(matches) - 1; i >= 0; i-- {
			match := matches[i]
			inner := text[match[2]:match[3]]
			name, fallback, hasDefault := strings.Cut(inner, ":")
			if !propertyName.MatchString(name) {
				return "", invalidConfiguration("Spring configuration has an invalid property placeholder name")
			}
			value := fallback
			if _, exists := c.raw(name); exists {
				var err error
				value, err = c.expand(name, visiting)
				if err != nil {
					return "", err
				}
			} else if !hasDefault {
				return "", &missingInput{name: name}
			}
			text = text[:match[0]] + value + text[match[1]:]
		}
	}
	if strings.Contains(text, "${") {
		return "", invalidConfiguration("Spring substitution exceeds 32 levels")
	}
	return text, nil
}

func (c *configuration) placeholderReferences() map[string][]Evidence {
	result := map[string][]Evidence{}
	for _, p := range c.values {
		references := map[string]bool{}
		c.collectPlaceholderReferences(p.text, references, map[string]bool{})
		evidence := Evidence{Property: p.key, Source: p.source}
		for name := range references {
			result[name] = append(result[name], evidence)
		}
	}
	return result
}

func (c *configuration) collectPlaceholderReferences(
	text string, references map[string]bool, visiting map[string]bool,
) {
	for _, match := range placeholder.FindAllStringSubmatch(text, -1) {
		name, _, _ := strings.Cut(match[1], ":")
		if !propertyName.MatchString(name) {
			continue
		}
		key := canonical(name)
		references[key] = true
		if visiting[key] {
			continue
		}
		if p, ok := c.values[key]; ok {
			visiting[key] = true
			c.collectPlaceholderReferences(p.text, references, visiting)
			delete(visiting, key)
		}
	}
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
		return 0, invalidConfiguration("%s must be an integer", key)
	}
	return value, nil
}
