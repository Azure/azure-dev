package auth

import "context"

type EnsureLoginGuard struct{}

func NewEnsureLoginGuard(manager *Manager, ctx context.Context) (EnsureLoginGuard, error) {
	cred, err := manager.CredentialForCurrentUser(ctx, nil)
	if err != nil {
		return EnsureLoginGuard{}, err
	}

	_, err = EnsureLoggedInCredential(ctx, cred)
	if err != nil {
		return EnsureLoginGuard{}, err
	}

	return EnsureLoginGuard{}, nil
}
