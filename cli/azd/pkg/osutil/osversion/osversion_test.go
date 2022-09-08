package osversion

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetVersion(t *testing.T) {
	ver, err := GetVersion()

	assert.NoError(t, err)
	assert.NotEmpty(t, ver)
}
