AZ_VERSION=${VERSION:-"latest"}

echo "(*) Installing Azure Dev CLI"

curl -fsSLO https://aka.ms/install-azd.sh
chmod +x ./install-azd.sh

./install-azd.sh --version $AZ_VERSION
