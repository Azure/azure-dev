DATABASE_FQDN=$1
APPLICATION_IDENTITY_APPID=$2
DATABASE_NAME=$3
AAD_ADMIN_USER_NAME=$4
CUSTOM_ROLE=$5

sleep 60;

echo "PostgreSQL Server creating AD role in database ${DATABASE_NAME} on ${DATABASE_FQDN}..."

if [ "${ARM_CLIENT_ID}" != "" ] && [ "${ARM_CLIENT_SECRET}" != "" ] && [ "${ARM_TENANT_ID}" != "" ];then
  az login --service-principal -u ${ARM_CLIENT_ID} -p ${ARM_CLIENT_SECRET} --tenant ${ARM_TENANT_ID}
fi

RDBMS_ACCESS_TOKEN=$(az account get-access-token --resource-type oss-rdbms --output tsv --query accessToken)

psql "host=${DATABASE_FQDN} user=${AAD_ADMIN_USER_NAME} dbname=postgres port=5432 password=${RDBMS_ACCESS_TOKEN} sslmode=require" <<EOF

select * from pgaadauth_create_principal_with_oid('${CUSTOM_ROLE}', '${APPLICATION_IDENTITY_APPID}', 'service', false, false);

EOF