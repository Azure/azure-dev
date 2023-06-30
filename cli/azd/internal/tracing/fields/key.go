package fields

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

func StringHashed(k attribute.Key, v string) attribute.KeyValue {
	return attribute.KeyValue{
		Key:   attribute.Key(k),
		Value: attribute.StringValue(CaseInsensitiveHash(v)),
	}
}

func StringSliceHashed(k attribute.Key, v []string) attribute.KeyValue {
	val := CaseInsensitiveSliceHash(v)
	return attribute.KeyValue{
		Key:   attribute.Key(k),
		Value: attribute.StringSliceValue(val),
	}
}

func CaseInsensitiveSliceHash(value []string) []string {
	hashed := make([]string, len(value))
	for i := range value {
		hashed[i] = CaseInsensitiveHash(value[i])
	}
	return hashed
}

func CaseInsensitiveHash(value string) string {
	return Sha256Hash(strings.ToLower(value))
}

// Sha256Hash returns the hex-encoded Sha256 hash of the given string.
func Sha256Hash(val string) string {
	sha := sha256.Sum256([]byte(val))
	hash := hex.EncodeToString(sha[:])
	return hash
}

// ErrorKey returns a new Key with "error." prefix appended.
func ErrorKey(k attribute.Key) attribute.Key {
	return attribute.Key("error." + string(k))
}
