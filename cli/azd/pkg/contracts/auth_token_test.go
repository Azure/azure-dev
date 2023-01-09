package contracts

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRFC3339TimeJson(t *testing.T) {
	tm, err := time.Parse(time.RFC3339Nano, "2023-01-09T06:39:00.313323855Z")

	require.NoError(t, err)

	stdRes, err := json.Marshal(tm)
	require.NoError(t, err)

	cusRes, err := json.Marshal(RFC3339Time(tm))
	require.NoError(t, err)

	assert.Equal(t, `"2023-01-09T06:39:00.313323855Z"`, string(stdRes))
	assert.Equal(t, `"2023-01-09T06:39:00Z"`, string(cusRes))
}
