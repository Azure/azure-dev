$env:DATABASE_FQDN=$Args[0]
$env:APPLICATION_IDENTITY_APPID=$Args[1]
$env:DATABASE_NAME=$Args[2]
$env:AAD_ADMIN_USER_NAME=$Args[3]
$env:CUSTOM_ROLE=$Args[4]

$env:CUSTOM_ROLE_CONSTANT='%%CUSTOM_ROLE%%'
$env:APPLICATION_IDENTITY_APPID_CONSTANT='%%APPLICATION_IDENTITY_APPID%%'

Start-Sleep 60;

"PostgreSQL Server creating AD role in database " + $env:DATABASE_NAME + " on " + $env:DATABASE_FQDN + "..."

(Get-Content scripts/create_ad_user.sql).replace($env:CUSTOM_ROLE_CONSTANT, $env:CUSTOM_ROLE).replace($env:APPLICATION_IDENTITY_APPID_CONSTANT, $env:APPLICATION_IDENTITY_APPID) | Set-Content scripts/tmp_users_processed.sql
Get-Content scripts/tmp_users_processed.sql

$env:PGPASSWORD=$(az account get-access-token --resource-type oss-rdbms --output tsv --query accessToken)

psql -h $env:DATABASE_FQDN -U $env:AAD_ADMIN_USER_NAME -d postgres -p 5432 -a -f scripts/tmp_users_processed.sql

del scripts/tmp_users_processed.sql