package cmdsubst

import "context"

type joinedExecutor struct {
	executors []CommandExecutor
}

func Join(executors ...CommandExecutor) CommandExecutor {
	return &joinedExecutor{
		executors: executors,
	}
}

func (d *joinedExecutor) Run(ctx context.Context, commandName string, args []string) (bool, string, error) {
	for _, executor := range d.executors {
		ran, replaced, err := executor.Run(ctx, commandName, args)
		if ran || err != nil {
			return ran, replaced, err
		}

	}

	return false, "", nil
}
