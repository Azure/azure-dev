package custommaps

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBindingsMap(t *testing.T) {
	m := &WithOrder[struct{}]{}
	err := json.Unmarshal([]byte(`{ "a": {}, "c": {}, "b": {} }`), &m)
	assert.NoError(t, err)

	keys := m.OrderedKeys()
	assert.Len(t, keys, 3)
	assert.Equal(t, []string{"a", "c", "b"}, keys)
}
