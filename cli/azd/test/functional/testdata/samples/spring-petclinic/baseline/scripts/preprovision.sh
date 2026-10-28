#!/usr/bin/env sh
set -eu

values="$(azd env get-values --output json)"
mode_persisted="$(printf '%s' "$values" | jq -r 'has("SPRING_DATABASE_TYPE")')"
if [ "$mode_persisted" = true ]; then
  mode="$(printf '%s' "$values" | jq -r '.SPRING_DATABASE_TYPE')"
elif [ "${SPRING_DATABASE_TYPE+x}" = x ]; then
  mode="$SPRING_DATABASE_TYPE"
else
  mode=h2
fi
case "$mode" in
  h2|postgresql) ;;
  *) echo "SPRING_DATABASE_TYPE must be h2 or postgresql" >&2; exit 1 ;;
esac

persisted_present="$(printf '%s' "$values" | jq -r 'has("SPRING_DATABASE_PASSWORD")')"
persisted="$(printf '%s' "$values" | jq -r '.SPRING_DATABASE_PASSWORD // empty')"
provided="${SPRING_DATABASE_PASSWORD-}"
provided_present=false
if [ "${SPRING_DATABASE_PASSWORD+x}" = x ]; then
  provided_present=true
fi
if [ "$mode" = h2 ]; then
  if [ "$persisted_present" = true ] || [ "$provided_present" = true ]; then
    echo "SPRING_DATABASE_PASSWORD cannot be set in h2 mode" >&2
    exit 1
  fi
  azd env set SPRING_DATABASE_TYPE "$mode" >/dev/null
  exit 0
fi

if { [ "$persisted_present" = true ] && [ -z "$persisted" ]; } ||
  { [ "$provided_present" = true ] && [ -z "$provided" ]; }; then
  echo "SPRING_DATABASE_PASSWORD cannot be explicitly empty in PostgreSQL mode" >&2
  exit 1
fi
if [ "$persisted_present" = true ] && [ "$provided_present" = true ] &&
  [ "$persisted" != "$provided" ]; then
  echo "The supplied PostgreSQL password conflicts with the persisted value" >&2
  exit 1
fi

if [ "$persisted_present" = true ]; then
  password="$persisted"
else
  password="$provided"
  if [ "$provided_present" = false ]; then
    subscription="$(printf '%s' "$values" | jq -r '.AZURE_SUBSCRIPTION_ID // empty')"
    env_name="$(printf '%s' "$values" | jq -r '.AZURE_ENV_NAME // empty')"
    if [ -z "$subscription" ] || [ -z "$env_name" ]; then
      echo "Cannot establish the selected Azure subscription and environment before generating a password" >&2
      exit 1
    fi
    deployment_count="$(
      az deployment sub list \
        --subscription "$subscription" \
        --query "[?tags.'azd-env-name' == '$env_name'] | length(@)" \
        --output tsv
    )"
    server_count="$(
      az resource list \
        --subscription "$subscription" \
        --tag "azd-env-name=$env_name" \
        --resource-type Microsoft.DBforPostgreSQL/flexibleServers \
        --query 'length(@)' \
        --output tsv
    )"
    if [ "$deployment_count" != 0 ] || [ "$server_count" != 0 ]; then
      echo "Existing infrastructure has no local password; restore the original SPRING_DATABASE_PASSWORD" >&2
      exit 1
    fi
    provided="AzD-$(openssl rand -hex 12)-p9!"
    password="$provided"
  fi
fi

if [ "${#password}" -lt 8 ] || [ "${#password}" -gt 128 ] ||
  ! printf '%s' "$password" | grep -q '[A-Z]' ||
  ! printf '%s' "$password" | grep -q '[a-z]' ||
  ! printf '%s' "$password" | grep -q '[0-9]' ||
  ! printf '%s' "$password" | grep -q '[^A-Za-z0-9]' ||
  printf '%s' "$password" | grep -q "['\"\\\\]"; then
  echo "SPRING_DATABASE_PASSWORD does not meet the PostgreSQL password requirements" >&2
  exit 1
fi

azd env set SPRING_DATABASE_TYPE "$mode" >/dev/null
if [ "$persisted_present" = false ]; then
  azd env set SPRING_DATABASE_PASSWORD "$password" >/dev/null
fi
