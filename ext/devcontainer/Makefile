.PHONY: test

help: ## show this help
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
	| awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%s\033[0m|%s\n", $$1, $$2}' \
	| column -t -s '|'

install-devcontainer-cli:
	@if ! [ -x "$$(command -v devcontainer)" ]; then echo "Installing devcontainer CLI"; npm install -g @devcontainers/cli; else echo "devcontainer CLI is already installed"; fi

test: install-devcontainer-cli ## run tests
	devcontainer features test --base-image mcr.microsoft.com/devcontainers/base:ubuntu
