package ioc

type ServiceLocator interface {
	Resolve(instance any) error
	ResolveNamed(name string, instance any) error
}

type serviceLocator struct {
	ioc *NestedContainer
}

func NewServiceLocator(ioc *NestedContainer) ServiceLocator {
	return &serviceLocator{
		ioc: ioc,
	}
}

func (sl *serviceLocator) Resolve(instance any) error {
	return sl.ioc.Resolve(instance)
}

func (sl *serviceLocator) ResolveNamed(name string, instance any) error {
	return sl.ioc.ResolveNamed(name, instance)
}
