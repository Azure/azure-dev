package account

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubscriptionsCache(t *testing.T) {
	dir := t.TempDir()
	s := &subscriptionsCache{
		cacheDir:     dir,
		inMemoryCopy: map[string][]Subscription{},
	}
	ctx := context.Background()

	// Empty state
	// Load items returns "not exist"
	_, err := s.Load(ctx, "key1")
	require.ErrorIs(t, err, os.ErrNotExist)

	_, err = s.Load(ctx, "key2")
	require.ErrorIs(t, err, os.ErrNotExist)

	// Clear items does not fail
	err = s.Clear(ctx)
	require.NoError(t, err)

	// Save items
	err = s.Save(ctx, "key1", []Subscription{{Id: "1", Name: "sub1"}})
	require.NoError(t, err)

	err = s.Save(ctx, "key2", []Subscription{{Id: "2", Name: "sub2"}})
	require.NoError(t, err)

	// Load items
	load, err := s.Load(ctx, "key1")
	require.NoError(t, err)
	require.Equal(t, "1", load[0].Id)

	load, err = s.Load(ctx, "key2")
	require.NoError(t, err)
	require.Equal(t, "2", load[0].Id)

	// Update items with Save and Load
	err = s.Save(ctx, "key1", []Subscription{{Id: "1", Name: "sub1-updated"}})
	require.NoError(t, err)

	load, err = s.Load(ctx, "key1")
	require.NoError(t, err)
	require.Equal(t, "1", load[0].Id)
	require.Equal(t, "sub1-updated", load[0].Name)

	// Clear items
	err = s.Clear(ctx)
	require.NoError(t, err)

	_, err = s.Load(ctx, "key1")
	require.ErrorIs(t, err, os.ErrNotExist)

	_, err = s.Load(ctx, "key2")
	require.ErrorIs(t, err, os.ErrNotExist)
}
