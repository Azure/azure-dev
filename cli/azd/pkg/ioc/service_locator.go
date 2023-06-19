package ioc

type ServiceLocator interface {
	Resolve(instance any) error
	ResolveNamed(name string, instance any) error
}

type serviceLocator struct {
	container *NestedContainer
}

func NewServiceLocator(container *NestedContainer) ServiceLocator {
	return &serviceLocator{
		container: container,
	}
}

func (s *serviceLocator) Resolve(instance any) error {
	return s.container.Resolve(instance)
}

func (s *serviceLocator) ResolveNamed(name string, instance any) error {
	return s.container.ResolveNamed(name, instance)
}
