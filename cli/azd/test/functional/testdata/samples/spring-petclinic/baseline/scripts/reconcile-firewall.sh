#!/usr/bin/env sh
set -eu

values="$(azd env get-values --output json)"
mode="$(printf '%s' "$values" | jq -r '.SPRING_DATABASE_TYPE // "h2"')"
[ "$mode" = postgresql ] || exit 0

app_id="$(printf '%s' "$values" | jq -r '.AZURE_CONTAINER_APP_ID // empty')"
server="$(printf '%s' "$values" | jq -r '.AZURE_POSTGRES_SERVER_NAME // empty')"
resource_group="$(printf '%s' "$values" | jq -r '.AZURE_RESOURCE_GROUP // empty')"
subscription="$(printf '%s' "$values" | jq -r '.AZURE_SUBSCRIPTION_ID // empty')"
if [ -z "$app_id" ] || [ -z "$server" ] || [ -z "$resource_group" ] || [ -z "$subscription" ]; then
  echo "Provisioned Container App or PostgreSQL identity is missing" >&2
  exit 1
fi

app_name="$(printf '%s' "$app_id" | awk -F/ '{print tolower($NF)}')"
prefix="azd-springboot-$(printf '%s' "$app_name" | sha256sum | cut -c1-8)-"
ips="$(az containerapp show --ids "$app_id" --query properties.outboundIpAddresses -o tsv)"
[ -n "$ips" ] || { echo "The Container App reports no outbound IP addresses" >&2; exit 1; }

desired=''
for ip in $ips; do
  name="${prefix}$(printf '%s' "$ip" | tr . -)"
  desired="$desired $name"
  az postgres flexible-server firewall-rule create \
    --subscription "$subscription" --resource-group "$resource_group" --name "$server" \
    --rule-name "$name" --start-ip-address "$ip" --end-ip-address "$ip" --output none
done

owned="$(az postgres flexible-server firewall-rule list \
  --subscription "$subscription" --resource-group "$resource_group" --name "$server" \
  --query "[?starts_with(name, '$prefix')].name" -o tsv)"
for name in $owned; do
  case " $desired " in
    *" $name "*) ;;
    *) az postgres flexible-server firewall-rule delete \
         --subscription "$subscription" --resource-group "$resource_group" --name "$server" \
         --rule-name "$name" --yes --output none ;;
  esac
done
