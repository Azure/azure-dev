// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSkillMd_Valid(t *testing.T) {
	doc := strings.Join([]string{
		"---",
		"name: my-skill",
		"description: Greets the user",
		"metadata:",
		"  owner: alice",
		"---",
		"# Skill body",
		"",
		"Greet the user warmly.",
	}, "\n")

	parsed, err := ParseSkillMd([]byte(doc))
	require.NoError(t, err)
	require.Equal(t, "my-skill", parsed.Name)
	require.Equal(t, "Greets the user", parsed.Description)
	require.Equal(t, map[string]string{"owner": "alice"}, parsed.Metadata)
	require.True(t, strings.HasPrefix(parsed.Instructions, "# Skill body"))
}

func TestParseSkillMd_NoFrontMatter(t *testing.T) {
	_, err := ParseSkillMd([]byte("Just some Markdown body without front matter.\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "must begin with a YAML front matter block")
}

func TestParseSkillMd_MissingCloseDelimiter(t *testing.T) {
	doc := "---\nname: foo\n# body but no closing ---\n"
	_, err := ParseSkillMd([]byte(doc))
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing its closing '---' delimiter")
}

func TestParseSkillMd_Empty(t *testing.T) {
	_, err := ParseSkillMd(nil)
	require.Error(t, err)
}

func TestParseSkillMd_InvalidYAML(t *testing.T) {
	doc := "---\nname: [unterminated\n---\nbody\n"
	_, err := ParseSkillMd([]byte(doc))
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse SKILL.md front matter")
}

func TestParseSkillMd_NonStringField(t *testing.T) {
	doc := "---\nname: 123\n---\nbody\n"
	_, err := ParseSkillMd([]byte(doc))
	require.Error(t, err)
	require.Contains(t, err.Error(), `field "name" must be a string`)
}

func TestParseSkillMd_BodyOnly(t *testing.T) {
	// Front matter present but empty body is allowed: instructions is "".
	doc := "---\nname: my-skill\ndescription: Just metadata\n---\n"
	parsed, err := ParseSkillMd([]byte(doc))
	require.NoError(t, err)
	require.Equal(t, "my-skill", parsed.Name)
	require.Equal(t, "Just metadata", parsed.Description)
	require.Equal(t, "", parsed.Instructions)
}

func TestParseSkillMd_LeadingBlankLines(t *testing.T) {
	doc := "\n\n---\nname: my-skill\n---\nbody\n"
	parsed, err := ParseSkillMd([]byte(doc))
	require.NoError(t, err)
	require.Equal(t, "my-skill", parsed.Name)
}

func TestParseSkillMd_CRLFLineEndings(t *testing.T) {
	doc := "---\r\nname: my-skill\r\ndescription: works\r\n---\r\nbody\r\n"
	parsed, err := ParseSkillMd([]byte(doc))
	require.NoError(t, err)
	require.Equal(t, "my-skill", parsed.Name)
	require.Equal(t, "works", parsed.Description)
}
