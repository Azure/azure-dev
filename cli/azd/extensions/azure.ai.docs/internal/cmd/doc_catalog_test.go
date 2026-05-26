// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseFrontMatter / stripFrontMatter cover the parser itself; tests
// here drive byte buffers directly so we can exercise edge cases
// without needing a real file under the embedded FS.

func TestParseFrontMatter_ParsesShortAndOrder(t *testing.T) {
	t.Parallel()
	in := []byte("---\nshort: hello\norder: 5\n---\nbody line\n")
	fm, bodyStart, err := parseFrontMatter(in)
	require.NoError(t, err)
	assert.Equal(t, "hello", fm.Short)
	require.NotNil(t, fm.Order)
	assert.Equal(t, 5, *fm.Order)
	assert.Equal(t, "body line\n", string(in[bodyStart:]))
}

func TestParseFrontMatter_NoFrontMatterReturnsZeroBodyOffset(t *testing.T) {
	t.Parallel()
	in := []byte("# Title\nbody\n")
	fm, bodyStart, err := parseFrontMatter(in)
	require.NoError(t, err)
	assert.Equal(t, "", fm.Short)
	assert.Nil(t, fm.Order)
	assert.Equal(t, 0, bodyStart)
}

func TestParseFrontMatter_RejectsMissingClosingFence(t *testing.T) {
	t.Parallel()
	in := []byte("---\nshort: hello\norder: 5\n# no closing fence\n")
	_, _, err := parseFrontMatter(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matching closing fence")
}

func TestParseFrontMatter_RejectsMalformedYAML(t *testing.T) {
	t.Parallel()
	in := []byte("---\nshort: [unclosed bracket\n---\nbody\n")
	_, _, err := parseFrontMatter(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode YAML")
}

// TestParseFrontMatter_RejectsUnknownField is the regression for the
// "typo silently sorts a topic incorrectly" hazard (rubber-duck #3).
// yaml.v3 ignores unknown fields by default; KnownFields(true) makes
// them fail loudly so the developer catches the typo at startup.
func TestParseFrontMatter_RejectsUnknownField(t *testing.T) {
	t.Parallel()
	in := []byte("---\nshort: hello\nordr: 5\n---\nbody\n")
	_, _, err := parseFrontMatter(in)
	require.Error(t, err)
	// yaml.v3 message: "field ordr not found in type cmd.frontMatter"
	assert.Contains(t, err.Error(), "ordr")
}

func TestParseFrontMatter_StripsUTF8BOM(t *testing.T) {
	t.Parallel()
	bom := "\uFEFF"
	in := []byte(bom + "---\nshort: hello\n---\nbody\n")
	fm, bodyStart, err := parseFrontMatter(in)
	require.NoError(t, err)
	assert.Equal(t, "hello", fm.Short)
	assert.Equal(t, "body\n", string(in[bodyStart:]))
}

func TestParseFrontMatter_SupportsCRLF(t *testing.T) {
	t.Parallel()
	in := []byte("---\r\nshort: hello\r\norder: 5\r\n---\r\nbody line\r\n")
	fm, bodyStart, err := parseFrontMatter(in)
	require.NoError(t, err)
	assert.Equal(t, "hello", fm.Short)
	require.NotNil(t, fm.Order)
	assert.Equal(t, 5, *fm.Order)
	// Body output preserves the CRLF on the body line.
	assert.Equal(t, "body line\r\n", string(in[bodyStart:]))
}

func TestParseFrontMatter_OrderZeroIsExplicit(t *testing.T) {
	t.Parallel()
	in := []byte("---\nshort: hello\norder: 0\n---\nbody\n")
	fm, _, err := parseFrontMatter(in)
	require.NoError(t, err)
	require.NotNil(t, fm.Order, "Order should be *int distinguishing 0 from absent")
	assert.Equal(t, 0, *fm.Order)
}

func TestParseFrontMatter_OrderAbsentIsNil(t *testing.T) {
	t.Parallel()
	in := []byte("---\nshort: hello\n---\nbody\n")
	fm, _, err := parseFrontMatter(in)
	require.NoError(t, err)
	assert.Nil(t, fm.Order, "absent Order should be nil so loader can fall back to orderFallback")
}

func TestStripFrontMatter_StripsFrontMatterBlock(t *testing.T) {
	t.Parallel()
	in := []byte("---\nshort: hello\n---\nbody line\n")
	got := stripFrontMatter(in)
	assert.Equal(t, "body line\n", string(got))
}

func TestStripFrontMatter_LeavesContentWithoutFrontMatterUnchanged(t *testing.T) {
	t.Parallel()
	in := []byte("# Title\nbody\n")
	got := stripFrontMatter(in)
	assert.Equal(t, "# Title\nbody\n", string(got))
}

func TestStripFrontMatter_StripsBOMEvenWithoutFrontMatter(t *testing.T) {
	t.Parallel()
	bom := "\uFEFF"
	in := []byte(bom + "# Title\nbody\n")
	got := stripFrontMatter(in)
	assert.Equal(t, "# Title\nbody\n", string(got))
}

// TestStripFrontMatter_PreservesBodyByteForByte is the regression for
// rubber-duck #C: the body output must be byte-identical to the source
// from the post-fence position through EOF. A refactor that
// accidentally munged whitespace or line endings would break callers
// that compare topic output to known fixtures.
func TestStripFrontMatter_PreservesBodyByteForByte(t *testing.T) {
	t.Parallel()
	// Intentional mix of blank lines, indentation, and trailing
	// whitespace -- a refactor that "normalizes" body output would
	// trip this test.
	body := "# Title\n\n  indented line\nline with trailing spaces   \n\n\nfinal\n"
	in := []byte("---\nshort: x\norder: 1\n---\n" + body)
	got := stripFrontMatter(in)
	assert.Equal(t, body, string(got))
}

// TestLoadCategoryTopics_ReadsAgentCategoryFromEmbeddedFS is a smoke
// test that the loader actually returns the shipped agent topics.
func TestLoadCategoryTopics_ReadsAgentCategoryFromEmbeddedFS(t *testing.T) {
	t.Parallel()
	topics, err := loadCategoryTopics("agent")
	require.NoError(t, err)
	names := make([]string, 0, len(topics))
	for _, top := range topics {
		names = append(names, top.Name)
	}
	// Ordering asserted in a separate test; here just check the set.
	for _, want := range []string{
		"samples", "initialize", "develop", "configure", "extend",
		"deploy", "evaluate", "operate", "investigate",
	} {
		assert.True(t, contains(names, want), "missing topic %q (got %v)", want, names)
	}
}

func TestLoadCategoryTopics_SortsByOrderThenName(t *testing.T) {
	t.Parallel()
	topics, err := loadCategoryTopics("agent")
	require.NoError(t, err)
	require.Len(t, topics, 9)
	// Workflow order: samples=5, initialize=10, develop=15, configure=20,
	// extend=25, deploy=30, evaluate=35, operate=40, investigate=45.
	want := []string{
		"samples", "initialize", "develop", "configure", "extend",
		"deploy", "evaluate", "operate", "investigate",
	}
	got := make([]string, len(topics))
	for i, top := range topics {
		got[i] = top.Name
	}
	assert.Equal(t, want, got,
		"topics must follow workflow order from front-matter `order` field")
}

func TestLoadCategoryTopics_AllShippedTopicsHaveShort(t *testing.T) {
	t.Parallel()
	topics, err := loadCategoryTopics("agent")
	require.NoError(t, err)
	for _, top := range topics {
		assert.NotEmpty(t, top.Short, "topic %q is missing a `short:` front-matter value", top.Name)
		assert.NotEqual(t, orderFallback, top.Order,
			"topic %q is missing an `order:` front-matter value (fell back to %d); "+
				"explicit order keeps the workflow sequence stable",
			top.Name, orderFallback)
	}
}

// TestLoadCategoryTopics_OrdersAreUniqueAcrossShippedTopics is the
// shipped-catalog test (rubber-duck #3 + #A). Duplicate orders would
// rely on the Name tiebreaker to disambiguate, which silently
// resequences topics when descriptions are renamed -- defensive to
// reject duplicates at PR time.
func TestLoadCategoryTopics_OrdersAreUniqueAcrossShippedTopics(t *testing.T) {
	t.Parallel()
	topics, err := loadCategoryTopics("agent")
	require.NoError(t, err)
	seen := map[int]string{}
	for _, top := range topics {
		if prev, ok := seen[top.Order]; ok {
			t.Fatalf("topics %q and %q share order=%d; assign distinct values", prev, top.Name, top.Order)
		}
		seen[top.Order] = top.Name
	}
}

func TestFindCategory(t *testing.T) {
	t.Parallel()
	assert.NotNil(t, FindCategory("agent"))
	assert.Nil(t, FindCategory("nonexistent"))
}

// contains is a small helper to keep tests readable.
func contains(haystack []string, needle string) bool {
	return strings.Contains(strings.Join(haystack, "|"), needle)
}
