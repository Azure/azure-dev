package kubectl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/braydonk/yaml"
	"github.com/sethvargo/go-retry"
)

var (
	ErrResourceNotFound = errors.New("cannot find resource")
	ErrResourceNotReady = errors.New("resource is not ready")
)

func GetResource[T any](
	ctx context.Context,
	cli *Cli,
	resourceType ResourceType,
	resourceName string,
	flags *KubeCliFlags,
) (T, error) {
	if flags == nil {
		flags = &KubeCliFlags{}
	}

	if flags.Output == "" {
		flags.Output = OutputTypeJson
	}

	var resource T

	res, err := cli.Exec(ctx, flags, "get", string(resourceType), resourceName)
	if err != nil {
		return resource, fmt.Errorf("failed getting resources, %w", err)
	}

	switch flags.Output {
	case OutputTypeJson:
		err = json.Unmarshal([]byte(res.Stdout), &resource)
		if err != nil {
			return resource, fmt.Errorf("failed unmarshalling resources JSON, %w", err)
		}
	case OutputTypeYaml:
		err = yaml.Unmarshal([]byte(res.Stdout), &resource)
		if err != nil {
			return resource, fmt.Errorf("failed unmarshalling resources YAML, %w", err)
		}
	default:
		return resource, fmt.Errorf("failed unmarshalling resources. Output format '%s' is not supported", flags.Output)
	}

	return resource, nil
}

func GetResources[T any](
	ctx context.Context,
	cli *Cli,
	resourceType ResourceType,
	flags *KubeCliFlags,
) (*List[T], error) {
	if flags == nil {
		flags = &KubeCliFlags{}
	}

	if flags.Output == "" {
		flags.Output = OutputTypeJson
	}

	res, err := cli.Exec(ctx, flags, "get", string(resourceType))
	if err != nil {
		return nil, fmt.Errorf("failed getting resources, %w", err)
	}

	var list List[T]

	switch flags.Output {
	case OutputTypeJson:
		err = json.Unmarshal([]byte(res.Stdout), &list)
		if err != nil {
			return nil, fmt.Errorf("failed unmarshalling resources JSON, %w", err)
		}
	case OutputTypeYaml:
		err = yaml.Unmarshal([]byte(res.Stdout), &list)
		if err != nil {
			return nil, fmt.Errorf("failed unmarshalling resources YAML, %w", err)
		}
	default:
		return nil, fmt.Errorf("failed unmarshalling resources. Output format '%s' is not supported", flags.Output)
	}

	return &list, nil
}

type ResourceFilterFn[T comparable] func(resource T) bool

func WaitForResource[T comparable](
	ctx context.Context,
	cli *Cli,
	resourceType ResourceType,
	resourceFilter ResourceFilterFn[T],
	readyStatusFilter ResourceFilterFn[T],
) (T, error) {
	var resource T
	var zero T
	err := retry.Do(
		ctx,
		retry.WithMaxDuration(time.Minute*10, retry.NewConstant(time.Second*10)),
		func(ctx context.Context) error {
			result, err := GetResources[T](ctx, cli, resourceType, nil)

			if err != nil {
				return fmt.Errorf("failed waiting for resource, %w", err)
			}

			for _, r := range result.Items {
				if resourceFilter(r) {
					resource = r
					break
				}
			}

			if resource == zero {
				return fmt.Errorf("cannot find resource for '%s', %w", resourceType, ErrResourceNotFound)
			}

			if !readyStatusFilter(resource) {
				return retry.RetryableError(fmt.Errorf("resource '%s' is not ready, %w", resourceType, ErrResourceNotReady))
			}

			return nil
		},
	)

	if err != nil {
		return zero, fmt.Errorf("failed waiting for resource, %w", err)
	}

	return resource, nil
}
