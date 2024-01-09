package ioc

type Lifetime string

const (
	Singleton Lifetime = "Singleton"
	Scoped    Lifetime = "Scoped"
	Transient Lifetime = "Transient"
)

type binding struct {
	name     string
	resolver any
	lifetime Lifetime
}
