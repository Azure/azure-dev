AZ_VERSION=${VERSION:-"latest"}

check_packages() {
    if ! dpkg -s "$@" > /dev/null 2>&1; then
        if [ "$(find /var/lib/apt/lists/* | wc -l)" = "0" ]; then
            echo "Running apt-get update..."
            apt-get update -y
        fi
        apt-get -y install --no-install-recommends "$@"
    fi
}

echo "(*) Ensuring dependencies are installed"

check_packages apt-transport-https curl ca-certificates gnupg2 dirmngr

echo "(*) Installing Azure Dev CLI"

curl -fsSL https://aka.ms/uninstall-azd.sh | bash -s -- --version $AZ_VERSION
