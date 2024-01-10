package ioc

type lifetime string

const (
	// Singleton lifetime indicates that the IoC container should only
	// create a single instance of the registered type.
	Singleton lifetime = "Singleton"
	// Scoped lifetime indicates that the IoC container should create a
	// single instance of the registered type per scope
	Scoped lifetime = "Scoped"
	// Transient lifetime indicates that the IoC container should create a
	// new instance of the registered type each time it is requested.
	Transient lifetime = "Transient"
)

// binding represents the metadata used for an IoC registration consisting of a optional name, lifetime and resolver.
type binding struct {
	name     string
	resolver any
	lifetime lifetime
}
