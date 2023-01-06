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
	val := make([]string, len(v))
	for i := range v {
		val[i] = CaseInsensitiveHash(v[i])
	}
	return attribute.KeyValue{
		Key:   attribute.Key(k),
		Value: attribute.StringSliceValue(val),
	}
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
