// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"gopkg.in/yaml.v3"
)

type springProperty struct {
	value  string
	source string
}

type springProperties map[string]springProperty

type springConfigInput struct {
	root         string
	jarPath      string
	packaged     *springArchive
	environment  map[string]string
	arguments    []string
	jvm          []string
	deployed     bool
	allowPending bool
}

type springConfiguration struct {
	properties       springProperties
	environment      map[string]string
	profiles         []string
	packaged         *springDependencies
	allowPending     bool
	selectionPending bool
}

type springConfigLoader struct {
	ctx       context.Context
	input     springConfigInput
	profiles  []string
	values    springProperties
	loading   map[string]bool
	loaded    map[string]bool
	count     int
	overrides springProperties
}

const hikariPropertiesPrefix = "spring.datasource.hikari.data-source-properties"

var errPendingSpringSelection = errors.New("Spring configuration selection awaits deployment values")

func springPropertyKey(key string) string {
	if bracket := strings.IndexByte(key, '['); bracket >= 0 &&
		compactSpringKey(key[:bracket]) == compactSpringKey(hikariPropertiesPrefix) {
		return compactSpringKey(hikariPropertiesPrefix) + ":" +
			strings.TrimSuffix(key[bracket+1:], "]")
	}
	parts := strings.SplitN(key, ".", 5)
	if len(parts) == 5 && compactSpringKey(strings.Join(parts[:4], ".")) == compactSpringKey(hikariPropertiesPrefix) {
		return compactSpringKey(hikariPropertiesPrefix) + ":" + parts[4]
	}
	if strings.HasPrefix(key, "spring.profiles.group.") {
		return compactSpringKey("spring.profiles.group") + ":" + strings.TrimPrefix(key, "spring.profiles.group.")
	}
	return compactSpringKey(key)
}

func normalizeSpringProperties(raw springProperties) (springProperties, error) {
	result := springProperties{}
	for _, key := range slices.Sorted(maps.Keys(raw)) {
		canonical := springPropertyKey(key)
		if existing, present := result[canonical]; present && existing.value != raw[key].value {
			return nil, configurationError("Spring property aliases in one source contain conflicting values")
		}
		result[canonical] = raw[key]
	}
	return result, nil
}

func compactSpringKey(key string) string {
	return strings.ToLower(strings.NewReplacer(".", "", "-", "", "_", "").Replace(key))
}

func (p springProperties) get(key string) (string, bool) {
	value, present := p[springPropertyKey(key)]
	return value.value, present
}

func (p springProperties) list(key string) []string {
	value, _ := p.get(key)
	result := splitSpringList(value)
	for i := 0; ; i++ {
		item, found := p.get(fmt.Sprintf("%s[%d]", key, i))
		if !found {
			break
		}
		result = append(result, splitSpringList(item)...)
	}
	return result
}

func splitSpringList(value string) []string {
	var result []string
	for part := range strings.SplitSeq(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func loadSpringConfiguration(ctx context.Context, input springConfigInput) (*springConfiguration, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.root == "" {
		return nil, configurationError("a Spring service project directory is required")
	}
	root, err := filepath.Abs(input.root)
	if err != nil {
		return nil, configurationError("the Spring service project directory cannot be resolved")
	}
	input.root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, configurationError("the Spring service project directory is unavailable")
	}
	if input.jarPath != "" {
		input.packaged, err = openSpringArchive(input.jarPath)
		if err != nil {
			return nil, err
		}
		defer input.packaged.reader.Close()
	}
	overrides, err := springConfigOverrides(input)
	if err != nil {
		return nil, err
	}
	var profiles []string
	initial := true
	seen := map[string]bool{}
	for range 24 {
		loader := &springConfigLoader{
			ctx: ctx, input: input, profiles: profiles, values: springProperties{},
			loading: map[string]bool{}, loaded: map[string]bool{},
			overrides: overrides,
		}
		if err := loader.loadLocations(overrides); err != nil {
			if errors.Is(err, errPendingSpringSelection) {
				configuration := &springConfiguration{
					properties: overrides, environment: input.environment,
					selectionPending: true, allowPending: true,
				}
				if input.packaged != nil {
					configuration.packaged = &input.packaged.dependencies
				}
				return configuration, nil
			}
			return nil, err
		}
		maps.Copy(loader.values, overrides)
		configuration := &springConfiguration{
			properties: loader.values, environment: input.environment, allowPending: input.allowPending,
		}
		if input.packaged != nil {
			configuration.packaged = &input.packaged.dependencies
		}
		next, err := configuration.activeProfiles()
		if err != nil {
			return nil, err
		}
		if configuration.selectionPending {
			return configuration, nil
		}
		if !initial && slices.Equal(profiles, next) {
			configuration.profiles = next
			return configuration, nil
		}
		fingerprint := strings.Join(next, "\x00")
		if seen[fingerprint] {
			return nil, configurationError("Spring profile selection contains a cycle")
		}
		seen[fingerprint] = true
		profiles = next
		initial = false
	}
	return nil, configurationError("Spring profile selection exceeds the supported depth")
}

func springConfigOverrides(input springConfigInput) (springProperties, error) {
	result := springProperties{}
	for _, key := range slices.Sorted(maps.Keys(input.environment)) {
		if key == "SPRING_APPLICATION_JSON" {
			continue
		}
		if strings.HasPrefix(compactSpringKey(key), compactSpringKey(hikariPropertiesPrefix)) {
			return nil, configurationError("Hikari driver map environment names cannot preserve case-sensitive driver keys")
		}
		canonical := springPropertyKey(springEnvironmentPropertyKey(key))
		if existing, present := result[canonical]; present && existing.value != input.environment[key] {
			return nil, configurationError("Spring environment aliases contain conflicting values")
		}
		result[canonical] = springProperty{input.environment[key], "container environment"}
	}
	var jvm []string
	for _, name := range []string{"JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS"} {
		options, err := splitJavaOptions(input.environment[name])
		if err != nil {
			return nil, err
		}
		jvm = append(jvm, options...)
	}
	jvm = append(jvm, input.jvm...)
	hotspotOptions, err := splitJavaOptions(input.environment["_JAVA_OPTIONS"])
	if err != nil {
		return nil, err
	}
	jvm = append(jvm, hotspotOptions...)
	for _, argument := range jvm {
		if strings.HasPrefix(argument, "-D") {
			key, value, _ := strings.Cut(argument[2:], "=")
			if key == "" {
				return nil, configurationError("a Spring JVM system property has no name")
			}
			result[springPropertyKey(key)] = springProperty{value, "JVM system properties"}
		}
	}
	applicationJSON := input.environment["SPRING_APPLICATION_JSON"]
	if value, present := result.get("spring.application.json"); present {
		applicationJSON = value
	}
	optionValues := map[string][]string{}
	for _, argument := range input.arguments {
		if !strings.HasPrefix(argument, "--") {
			continue
		}
		key, value, hasValue := strings.Cut(argument[2:], "=")
		if key == "" {
			return nil, configurationError("a Spring command-line property has no name")
		}
		values := optionValues[key]
		if hasValue {
			values = append(values, value)
		}
		optionValues[key] = values
	}
	rawArguments := springProperties{}
	for key, values := range optionValues {
		rawArguments[key] = springProperty{strings.Join(values, ","), "application arguments"}
	}
	arguments, err := normalizeSpringProperties(rawArguments)
	if err != nil {
		return nil, err
	}
	if value, present := arguments.get("spring.application.json"); present {
		applicationJSON = value
	}
	if applicationJSON != "" {
		var values map[string]any
		if err := json.Unmarshal([]byte(applicationJSON), &values); err != nil || values == nil {
			return nil, configurationError("SPRING_APPLICATION_JSON must be a JSON object")
		}
		raw := springProperties{}
		if err := flattenSpringValues("", values, raw, "SPRING_APPLICATION_JSON", true); err != nil {
			return nil, err
		}
		normalized, err := normalizeSpringProperties(raw)
		if err != nil {
			return nil, err
		}
		maps.Copy(result, normalized)
	}
	maps.Copy(result, arguments)
	return result, nil
}

func springEnvironmentPropertyKey(key string) string {
	if !strings.Contains(key, "_") {
		return key
	}
	var result strings.Builder
	for i, part := range strings.Split(strings.ToLower(key), "_") {
		if _, err := strconv.ParseUint(part, 10, 32); err == nil {
			result.WriteString("[" + part + "]")
		} else {
			if i != 0 {
				result.WriteByte('.')
			}
			result.WriteString(part)
		}
	}
	return result.String()
}

func splitJavaOptions(value string) ([]string, error) {
	var words []string
	var current strings.Builder
	var quote rune
	for _, r := range value {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			if current.Len() != 0 {
				words = append(words, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, configurationError("JVM option quoting cannot be resolved")
	}
	if current.Len() != 0 {
		words = append(words, current.String())
	}
	return words, nil
}

func (l *springConfigLoader) loadLocations(overrides springProperties) error {
	overrides = maps.Clone(overrides)
	for key, property := range overrides {
		for _, selector := range []string{
			"spring.config.name", "spring.config.location", "spring.config.additional-location", "spring.config.import",
		} {
			prefix := springPropertyKey(selector)
			if key == prefix || strings.HasPrefix(key, prefix+"[") {
				value, err := l.resolveSelector(property.value)
				if err != nil {
					return err
				}
				property.value = value
				overrides[key] = property
				break
			}
		}
	}
	names := overrides.list("spring.config.name")
	if len(names) == 0 {
		names = []string{"application"}
	}
	for _, name := range names {
		if name == "." || name == ".." || strings.ContainsAny(name, `/\:`) {
			return configurationError("spring.config.name must contain simple file names")
		}
	}
	locations := []string{"optional:classpath:/;optional:classpath:/config/"}
	if !l.input.deployed {
		locations = append(locations, "optional:file:./;optional:file:./config/;optional:file:./config/*/")
	}
	if value, set := overrides.get("spring.config.location"); set {
		locations = splitSpringList(value)
	}
	locations = append(locations, overrides.list("spring.config.additional-location")...)
	for _, group := range locations {
		for _, profile := range append([]string{""}, l.profiles...) {
			for _, location := range strings.Split(group, ";") {
				if err := l.loadLocation(location, "", names, profile); err != nil {
					return err
				}
			}
		}
	}
	for _, imported := range overrides.list("spring.config.import") {
		if err := l.loadLocation(imported, "", names, ""); err != nil {
			return err
		}
		for _, profile := range l.profiles {
			if err := l.loadLocation(imported, "", names, profile); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *springConfigLoader) loadLocation(location, relative string, names []string, profile string) error {
	if err := l.ctx.Err(); err != nil {
		return err
	}
	var err error
	location, err = l.resolveSelector(location)
	if err != nil {
		return err
	}
	optional := strings.HasPrefix(location, "optional:")
	location = strings.TrimPrefix(location, "optional:")
	if location == "file:./config/*/" && !l.input.deployed {
		base, err := l.resolveFile("file:./config/", "")
		if err != nil {
			return err
		}
		entries, err := os.ReadDir(base)
		if optional && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return configurationError("the Spring configuration wildcard directory cannot be read")
		}
		if len(entries) > 128 {
			return configurationError("Spring wildcard configuration exceeds the supported directory count")
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return configurationError("Spring wildcard configuration cannot follow unverified directory links")
			}
			if entry.IsDir() {
				if err := l.loadLocation("file:./config/"+entry.Name()+"/", "", names, profile); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if strings.HasSuffix(location, "/") {
		path, err := l.resolveFile(location, relative)
		if err != nil {
			return err
		}
		if l.input.packaged != nil && strings.HasPrefix(path, "classpath:/") {
			if !l.input.packaged.directory(strings.TrimPrefix(path, "classpath:/")) {
				if optional {
					return nil
				}
				return configurationError("a required packaged Spring configuration directory is missing")
			}
		} else {
			info, err := os.Stat(path)
			if optional && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil || !info.IsDir() {
				return configurationError("a required Spring configuration directory is missing")
			}
		}
		for _, name := range names {
			for _, extension := range []string{".yaml", ".yml", ".properties"} {
				filename := location + profileFileName(name+extension, profile)
				if err := l.loadFile(filename, relative, true, profile != ""); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return l.loadFile(profileFileName(location, profile), relative, optional || profile != "", profile != "")
}

func (l *springConfigLoader) resolveSelector(value string) (string, error) {
	properties := maps.Clone(l.values)
	maps.Copy(properties, l.overrides)
	config := &springConfiguration{properties: properties, environment: l.input.environment}
	resolved, err := config.expand(value, l.input.allowPending, map[string]bool{})
	if err != nil {
		return "", err
	}
	if strings.Contains(resolved, "${") {
		return "", errPendingSpringSelection
	}
	return resolved, nil
}

func profileFileName(name, profile string) string {
	if profile == "" {
		return name
	}
	extension := filepath.Ext(name)
	return strings.TrimSuffix(name, extension) + "-" + profile + extension
}

func (l *springConfigLoader) loadFile(location, relative string, optional, profileSpecific bool) error {
	if err := l.ctx.Err(); err != nil {
		return err
	}
	path, err := l.resolveFile(location, relative)
	if err != nil {
		return err
	}
	if l.loading[path] {
		return configurationError("Spring configuration imports contain a cycle")
	}
	if l.loaded[path] {
		return nil
	}
	data, err := l.readFile(path)
	if optional && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	l.count++
	if l.count > 128 {
		return configurationError("Spring configuration imports exceed the supported file count")
	}
	documents, err := decodeSpringDocuments(path, data)
	if err != nil {
		return err
	}
	l.loading[path] = true
	defer delete(l.loading, path)
	for _, document := range documents {
		if profileSpecific {
			for key := range document {
				if key == "spring.profiles.active" || key == "spring.profiles.include" ||
					strings.HasPrefix(key, "spring.profiles.group.") {
					return configurationError("profile-specific files cannot change active profiles")
				}
			}
		}
		if expression := document["spring.config.activate.on-profile"]; expression != "" {
			if l.profiles == nil {
				continue
			}
			selected, err := matchesSpringProfiles(expression, l.profiles)
			if err != nil {
				return configurationError("a Spring profile activation expression is invalid")
			}
			if !selected {
				continue
			}
			for key := range document {
				if key == "spring.profiles.active" || key == "spring.profiles.include" ||
					strings.HasPrefix(key, "spring.profiles.group.") {
					return configurationError("profile-activated documents cannot change active profiles")
				}
			}
		}
		if _, present := document["spring.config.activate.on-cloud-platform"]; present {
			return configurationError("cloud-platform activated Spring documents require runtime configuration evidence")
		}
		raw := springProperties{}
		for key, value := range document {
			raw[key] = springProperty{value, location}
		}
		normalized, err := normalizeSpringProperties(raw)
		if err != nil {
			return err
		}
		maps.Copy(l.values, normalized)
		for _, imported := range splitSpringList(document["spring.config.import"]) {
			if err := l.loadLocation(imported, path, []string{"application"}, ""); err != nil {
				return err
			}
			for _, profile := range l.profiles {
				if err := l.loadLocation(imported, path, []string{"application"}, profile); err != nil {
					return err
				}
			}
		}
	}
	l.loaded[path] = true
	return nil
}

func (l *springConfigLoader) resolveFile(location, relative string) (string, error) {
	if l.input.packaged != nil && (strings.HasPrefix(location, "classpath:") ||
		(!strings.Contains(location, ":") && strings.HasPrefix(relative, "classpath:/"))) {
		return springArchiveResource(location, relative)
	}
	var path string
	switch {
	case strings.HasPrefix(location, "classpath:"):
		path = filepath.Join(l.input.root, "src", "main", "resources",
			filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(location, "classpath:"), "/")))
	case strings.HasPrefix(location, "file:"):
		if l.input.deployed {
			return "", configurationError("external container files are not mapped to verified packaged configuration")
		}
		path = filepath.Join(l.input.root, filepath.FromSlash(strings.TrimPrefix(location, "file:")))
	case strings.Contains(location, ":"):
		return "", configurationError("remote or custom Spring configuration imports are not supported")
	case relative != "":
		path = filepath.Join(filepath.Dir(relative), filepath.FromSlash(location))
	default:
		return "", configurationError("Spring configuration locations require classpath: or file:")
	}
	path = filepath.Clean(path)
	if !osutil.IsPathContained(l.input.root, path) || strings.Contains(location, "*") {
		return "", configurationError("a Spring configuration path is unsupported or outside the service project")
	}
	return path, nil
}

func (l *springConfigLoader) readFile(path string) ([]byte, error) {
	if l.input.packaged != nil && strings.HasPrefix(path, "classpath:/") {
		return l.input.packaged.read(strings.TrimPrefix(path, "classpath:/"))
	}
	resolved, err := filepath.EvalSymlinks(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil || !osutil.IsPathContained(l.input.root, resolved) {
		return nil, configurationError("a Spring configuration file is unavailable or outside the service project")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, configurationError("a required Spring configuration file cannot be read")
	}
	data, readErr := readSpringConfigurationData(file)
	return data, errors.Join(readErr, file.Close())
}

func decodeSpringDocuments(path string, data []byte) ([]map[string]string, error) {
	if strings.HasSuffix(path, ".properties") {
		documents, err := parseSpringProperties(string(data))
		if err != nil {
			return nil, configurationError("invalid Spring properties configuration")
		}
		return documents, nil
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var documents []map[string]string
	for {
		var document map[string]any
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, configurationError("invalid Spring YAML configuration")
		}
		properties := springProperties{}
		if err := flattenSpringValues("", document, properties, path, false); err != nil {
			return nil, err
		}
		flat := map[string]string{}
		for key, value := range properties {
			flat[key] = value.value
		}
		documents = append(documents, flat)
	}
	return documents, nil
}

func flattenSpringValues(prefix string, value any, properties springProperties, source string, skipNull bool) error {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(typed)) {
			path := key
			if prefix != "" {
				path = prefix + "." + key
				if compactSpringKey(prefix) == compactSpringKey(hikariPropertiesPrefix) {
					path = prefix + "[" + key + "]"
				} else if strings.HasPrefix(prefix, hikariPropertiesPrefix+"[") && strings.HasSuffix(prefix, "]") {
					path = strings.TrimSuffix(prefix, "]") + "." + key + "]"
				}
			}
			if err := flattenSpringValues(path, typed[key], properties, source, skipNull); err != nil {
				return err
			}
		}
	case []any:
		for i, item := range typed {
			if err := flattenSpringValues(fmt.Sprintf("%s[%d]", prefix, i), item, properties, source, skipNull); err != nil {
				return err
			}
		}
	case nil:
		if !skipNull {
			properties[prefix] = springProperty{"", source}
		}
	case string, bool, int, int64, uint64, float64:
		properties[prefix] = springProperty{fmt.Sprint(typed), source}
	default:
		return configurationError("Spring configuration contains unsupported YAML values")
	}
	return nil
}

func (c *springConfiguration) activeProfiles() ([]string, error) {
	profiles := c.properties.list("spring.profiles.active")
	if len(profiles) == 0 {
		profiles = c.properties.list("spring.profiles.default")
		if len(profiles) == 0 {
			profiles = []string{"default"}
		}
	}
	profiles = append(c.properties.list("spring.profiles.include"), profiles...)
	result := []string{}
	var expand func(string, map[string]bool) error
	expand = func(name string, stack map[string]bool) error {
		resolved, err := c.expand(name, c.allowPending, map[string]bool{})
		if err != nil {
			return err
		}
		if strings.Contains(resolved, "${") {
			c.selectionPending = true
			return nil
		}
		name = resolved
		if name == "none" {
			return nil
		}
		if name == "" || strings.ContainsAny(name, ` /\!&|(),`) {
			return configurationError("an active Spring profile name is invalid")
		}
		if stack[name] {
			return configurationError("Spring profile groups contain a cycle")
		}
		if slices.Contains(result, name) {
			return nil
		}
		result = append(result, name)
		stack[name] = true
		for _, member := range c.properties.list("spring.profiles.group." + name) {
			if err := expand(member, stack); err != nil {
				return err
			}
		}
		delete(stack, name)
		return nil
	}
	for _, profile := range profiles {
		value, err := c.expand(profile, c.allowPending, map[string]bool{})
		if err != nil {
			return nil, err
		}
		if strings.Contains(value, "${") {
			c.selectionPending = true
			return nil, nil
		}
		if err := expand(value, map[string]bool{}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (c *springConfiguration) resolve(key string, pending bool) (string, bool, error) {
	value, present := c.properties.get(key)
	if !present {
		return "", false, nil
	}
	resolved, err := c.expand(value, pending, map[string]bool{key: true})
	return resolved, true, err
}

func (c *springConfiguration) expand(value string, pending bool, stack map[string]bool) (string, error) {
	if len(stack) > 32 {
		return "", configurationError("Spring property placeholders exceed the supported depth")
	}
	for {
		start := strings.Index(value, "${")
		if start < 0 {
			return value, nil
		}
		depth, end := 1, start+2
		for ; end < len(value) && depth != 0; end++ {
			if strings.HasPrefix(value[end:], "${") {
				depth++
				end++
			} else if value[end] == '}' {
				depth--
			}
		}
		if depth != 0 {
			return "", configurationError("a Spring property placeholder is not closed")
		}
		token := value[start+2 : end-1]
		name, fallback, hasDefault := strings.Cut(token, ":")
		if strings.Contains(name, "${") || name == "" {
			return "", configurationError("a Spring property placeholder name cannot be resolved")
		}
		if stack[name] {
			return "", configurationError("Spring property placeholders contain a cycle")
		}
		replacement, present := c.properties.get(name)
		if !present {
			replacement, present = c.environment[name]
		}
		if !present {
			if !hasDefault {
				if pending {
					return value, nil
				}
				return "", configurationError("a required Spring property placeholder is unresolved")
			}
			replacement = fallback
		}
		stack[name] = true
		replacement, err := c.expand(replacement, pending, stack)
		delete(stack, name)
		if err != nil {
			return "", err
		}
		if strings.Contains(replacement, "${") && pending {
			return value[:start] + replacement + value[end:], nil
		}
		value = value[:start] + replacement + value[end:]
	}
}

func configurationError(message string) error {
	return validationError("spring_configuration_unresolved", message,
		"provide unambiguous supported Spring configuration; configuration values are not included in diagnostics")
}

func springBoolean(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "on", "yes", "1":
		return true, nil
	case "false", "off", "no", "0":
		return false, nil
	default:
		return false, configurationError("a Spring authentication boolean is invalid")
	}
}

func springPort(value string) (int, error) {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, configurationError("the PostgreSQL datasource port is invalid")
	}
	return port, nil
}
