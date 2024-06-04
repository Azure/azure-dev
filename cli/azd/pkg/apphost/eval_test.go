package apphost

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEvalString(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{name: "simple", src: "a string with no replacements", want: "a string with no replacements"},
		{name: "replacement", src: "{this.one.has.a.replacement}", want: "this.one.has.a.replacement"},
		{name: "complex", src: "this {one} has {many} replacements", want: "this one has many replacements"},
		{name: "escape", src: "this {{one}} is {{escaped}}", want: "this {one} is {escaped}"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := EvalString(c.src, func(s string) (string, error) {
				return s, nil
			})

			assert.NoError(t, err)
			assert.Equal(t, c.want, res)
		})
	}

	errorCases := []struct {
		name string
		src  string
	}{
		{name: "unclosed open", src: "this { is unclosed"},
		{name: "unmatched close", src: "this } is unmatched"},
		{name: "unmatched escaped close", src: "this {}} is unmatched"},
		{name: "unmatched escaped open", src: "this {{} is unmatched"},
	}

	for _, c := range errorCases {
		t.Run(c.name, func(t *testing.T) {
			res, err := EvalString(c.src, func(s string) (string, error) {
				return s, nil
			})

			assert.Error(t, err)
			assert.Equal(t, "", res)
		})
	}

	res, err := EvalString("{this.one.has.a.replacement}", func(s string) (string, error) {
		return "", fmt.Errorf("this should cause evalString to fail")
	})

	assert.Error(t, err)
	assert.Equal(t, "", res)
}
