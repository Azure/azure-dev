package osversion

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetVersion(t *testing.T) {
	_, err := GetVersion()

	assert.NoError(t, err)
}
