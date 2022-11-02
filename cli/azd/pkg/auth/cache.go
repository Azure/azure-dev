package auth

import (
	"log"

	"github.com/AzureAD/microsoft-authentication-library-for-go/apps/cache"
)

// exportReplaceWithErrors is like cache.ExportReplace except each method is allowed to return an error.
type exportReplaceWithErrors interface {
	Replace(cache cache.Unmarshaler, key string) error
	Export(cache cache.Marshaler, key string) error
}

// errorDroppingCacheAdapter implements cache.ExportReplace using an instance of ExportReplaceWithErrors. Any errors
// encountered are reported to log.Printf.
type errorDroppingCacheAdapter struct {
	inner exportReplaceWithErrors
}

func (a *errorDroppingCacheAdapter) Export(cache cache.Marshaler, key string) {
	if err := a.inner.Export(cache, key); err != nil {
		log.Printf("ignoring error from cache implementation: %v", err)
	}
}

func (a *errorDroppingCacheAdapter) Replace(cache cache.Unmarshaler, key string) {
	if err := a.inner.Replace(cache, key); err != nil {
		log.Printf("ignoring error from cache implementation: %v", err)
	}
}
