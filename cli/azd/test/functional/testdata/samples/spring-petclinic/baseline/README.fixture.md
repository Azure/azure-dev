# Spring Petclinic native ACA baseline

Stage the complete `overlay` fixture first, then replace `azure.yaml` and add
the files from this directory. The resulting checkpoint uses native
`host: containerapp` without the Spring extension.

The checked-in Dockerfile and password/firewall hooks intentionally expose the
setup that `azure.springboot` replaces. The firewall hooks require an
authenticated Azure CLI in addition to azd and preserve unrelated rules. They
are for baseline comparison only, not the final experience. Password generation
fails closed when read-only Azure checks find an existing azd deployment or
tagged PostgreSQL server. Lost local state must be restored with the original
administrator password; the baseline does not rotate credentials.

Do not run the live baseline without explicit Azure-cost authorization.
