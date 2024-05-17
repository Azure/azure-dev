package custommaps

import (
	"bytes"
	"encoding/json"
)

// WithOrder is like map, but also retains information about the order the keys of the object where in
// when it was unmarshalled from JSON.
type WithOrder[T any] struct {
	innerMap map[string]*T
	keys     []string
}

// Keys returns the keys of the map in the order they were unmarshalled from JSON.
func (b *WithOrder[T]) OrderedKeys() []string {
	return b.keys
}

// OrderedValues returns the values of the map in the order they were unmarshalled from JSON.
func (b *WithOrder[T]) OrderedValues() []*T {
	values := make([]*T, len(b.keys))
	for i, key := range b.keys {
		values[i] = b.innerMap[key]
	}
	return values
}

// Get returns the value associated with the given key, and a boolean indicating whether the key was present.
func (b *WithOrder[T]) Get(key string) (*T, bool) {
	v, ok := b.innerMap[key]
	return v, ok
}

func (b *WithOrder[T]) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &b.innerMap); err != nil {
		return err
	}

	dec := json.NewDecoder(bytes.NewReader(data))

	// read the start of the object
	_, err := dec.Token()
	if err != nil {
		return err
	}

	for {
		// read key or end
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if tok == json.Delim('}') {
			return nil
		} else {
			b.keys = append(b.keys, tok.(string))
		}

		// read binding value (and discard it, we already unmarshalled it into b.bindings)
		var b T
		if err := dec.Decode(&b); err != nil {
			return err
		}
	}
}
