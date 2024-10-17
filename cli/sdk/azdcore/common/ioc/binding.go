package ioc

// binding represents the metadata used for an IoC registration consisting of a optional name and resolver.
type binding struct {
	name     string
	resolver any
}
