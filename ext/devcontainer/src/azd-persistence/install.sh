#!/usr/bin/env bash
#-------------------------------------------------------------------------------------------------------------
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License. See https://go.microsoft.com/fwlink/?linkid=2090316 for license information.
#-------------------------------------------------------------------------------------------------------------

echo "(*) Azure Developer CLI - Persistence"
echo "User: ${_REMOTE_USER}     User home: ${_REMOTE_USER_HOME}"

if [  -z "$_REMOTE_USER" ] || [ -z "$_REMOTE_USER_HOME" ]; then
  echo "***********************************************************************************"
  echo "*** Require _REMOTE_USER and _REMOTE_USER_HOME to be set (by dev container CLI) ***"
  echo "***********************************************************************************"
  exit 1
fi

if [ -e "$_REMOTE_USER_HOME/.azd" ]; then
  echo "Moving existing .azd folder to .azd-old"
  mv "$_REMOTE_USER_HOME/.azd" "$_REMOTE_USER_HOME/.azd-old"
fi

ln -s /dc/azd/ "$_REMOTE_USER_HOME/.azd"
chown -R "${_REMOTE_USER}:${_REMOTE_USER}" "$_REMOTE_USER_HOME/.azd"

# chown mount (only attached on startup)
cat << EOF >> "$_REMOTE_USER_HOME/.bashrc"
sudo chown -R "${_REMOTE_USER}:${_REMOTE_USER}" /dc/azd
EOF
chown -R $_REMOTE_USER $_REMOTE_USER_HOME/.bashrc
