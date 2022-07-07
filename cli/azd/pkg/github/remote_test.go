package github

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetGitHubSlugForRemote(t *testing.T) {
	cases := []struct {
		remote  string
		result  string
		isError bool
	}{
		{remote: "git@github.com:Foo/bar.git", result: "Foo/bar"},
		{remote: "https://github.com/Foo/bar.git", result: "Foo/bar"},
		{remote: "https://www.github.com/Foo/bar.git", result: "Foo/bar"},

		{remote: "git@github.com:Foo/bar", result: "Foo/bar"},
		{remote: "https://github.com/Foo/bar", result: "Foo/bar"},
		{remote: "https://www.github.com/Foo/bar", result: "Foo/bar"},

		{remote: "https://www.guthub.com/Foo/bar.git", isError: true},
		{remote: "not-a-remote", isError: true},
		{remote: "", isError: true},
	}

	for _, tst := range cases {
		slug, err := GetSlugForRemote(tst.remote)

		if tst.isError {
			require.Error(t, err, "expected error for %s", tst.remote)
		} else {
			require.NoError(t, err, "expected no error for %s", tst.remote)
		}

		assert.Equal(t, tst.result, slug, "expected equal for %s", tst.remote)
	}
}
