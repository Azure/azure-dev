// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"regexp"
	"slices"
	"strings"
)

// EmbeddedBinding is a reproducible, credential-free in-memory datasource binding.
type EmbeddedBinding struct {
	Engine        string
	URL           string
	URLConfigured bool
	Driver        string
	Evidence      []Evidence
}

var memoryDatabaseName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

var embeddedEngines = []struct {
	name     string
	group    string
	artifact string
	driver   string
	prefix   string
}{
	{"h2", "com.h2database", "h2", "org.h2.Driver", "jdbc:h2:mem:"},
	{"derby", "org.apache.derby", "derby", "org.apache.derby.jdbc.EmbeddedDriver", "jdbc:derby:memory:"},
	{"hsqldb", "org.hsqldb", "hsqldb", "org.hsqldb.jdbc.JDBCDriver", "jdbc:hsqldb:mem:"},
}

func resolveEmbedded(m *model, c *configuration) (*EmbeddedBinding, error) {
	jdbc, err := c.resolve("spring.datasource.url")
	if err != nil {
		return nil, err
	}
	trace := slices.Clone(c.traces["spring.datasource.url"])
	driver, err := c.resolve("spring.datasource.driver-class-name")
	if err != nil {
		return nil, err
	}
	for _, evidence := range c.traces["spring.datasource.driver-class-name"] {
		if !slices.Contains(trace, evidence) {
			trace = append(trace, evidence)
		}
	}
	for _, key := range []string{"spring.datasource.username", "spring.datasource.password"} {
		if _, err := c.resolve(key); err != nil {
			return nil, err
		}
	}
	if value, err := c.resolve("spring.datasource.embedded-database-connection"); err != nil {
		return nil, err
	} else if value != "" {
		return nil, unsupportedConfiguration("custom embedded database selection is not yet supported")
	}
	for _, engine := range embeddedEngines {
		if jdbc != "" && !strings.HasPrefix(jdbc, engine.prefix) ||
			jdbc == "" && !m.hasDependency(engine.group, engine.artifact) {
			continue
		}
		if driver != "" && driver != engine.driver {
			return nil, invalidConfiguration("the configured JDBC driver does not match the embedded datasource")
		}
		if jdbc == "" {
			// Let Spring retain its default database name and credentials.
			_, configured := c.raw("spring.datasource.url")
			return &EmbeddedBinding{
				Engine: engine.name, URLConfigured: configured, Driver: driver, Evidence: trace,
			}, nil
		}
		binding := &EmbeddedBinding{
			Engine: engine.name, URL: jdbc, URLConfigured: true, Driver: driver, Evidence: trace,
		}
		if err := validateEmbedded(binding); err != nil {
			return nil, err
		}
		return binding, nil
	}
	return nil, invalidConfiguration("the embedded datasource has no supported runtime driver")
}

func validateEmbedded(binding *EmbeddedBinding) error {
	for _, engine := range embeddedEngines {
		if binding.Engine != engine.name {
			continue
		}
		if binding.Driver != "" && binding.Driver != engine.driver {
			return invalidConfiguration("the configured JDBC driver does not match the embedded datasource")
		}
		if binding.URL == "" {
			return nil
		}
		if !strings.HasPrefix(binding.URL, engine.prefix) {
			return invalidConfiguration("the embedded datasource URL does not match its engine")
		}
		parts := strings.Split(strings.TrimPrefix(binding.URL, engine.prefix), ";")
		if !memoryDatabaseName.MatchString(parts[0]) {
			return unsupportedConfiguration("the in-memory database name contains unsupported characters")
		}
		seen := map[string]bool{}
		for _, option := range parts[1:] {
			key, value, ok := strings.Cut(option, "=")
			key, value = strings.ToUpper(key), strings.ToUpper(value)
			allowed := engine.name == "h2" &&
				(key == "DB_CLOSE_DELAY" && slices.Contains([]string{"-1", "0"}, value) ||
					key == "DB_CLOSE_ON_EXIT" && slices.Contains([]string{"TRUE", "FALSE"}, value)) ||
				engine.name == "derby" && key == "CREATE" && value == "TRUE"
			if !ok || !allowed || seen[key] {
				return unsupportedConfiguration("the embedded JDBC URL contains an unsupported or duplicate option")
			}
			seen[key] = true
		}
		return nil
	}
	return unsupportedConfiguration("the embedded datasource engine is unsupported")
}
