package account

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionsCache(t *testing.T) {
	dir := t.TempDir()
	s := &SubscriptionsCache{
		cacheDir:    dir,
		memoryCache: map[string][]Subscription{},
	}

	// Save items
	err := s.Save("key1", []Subscription{{Id: "1", Name: "sub1"}})
	require.NoError(t, err)

	err = s.Save("key2", []Subscription{{Id: "2", Name: "sub2"}})
	require.NoError(t, err)

	// Load items
	load, err := s.Load("key1")
	require.NoError(t, err)
	require.Equal(t, "1", load[0].Id)

	load, err = s.Load("key2")
	require.NoError(t, err)
	require.Equal(t, "2", load[0].Id)

	// Clear items
	err = s.Clear()
	require.NoError(t, err)

	_, err = s.Load("key1")
	require.ErrorIs(t, err, os.ErrNotExist)

	_, err = s.Load("key2")
	require.ErrorIs(t, err, os.ErrNotExist)
}
