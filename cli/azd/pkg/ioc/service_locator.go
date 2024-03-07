package ioc

type ServiceLocator interface {
	Resolve(instance any) error
	ResolveNamed(name string, instance any) error
}
