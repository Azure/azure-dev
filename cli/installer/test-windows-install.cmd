powershell -c "if ((Get-ExecutionPolicy) -ne 'Unrestricted') { Set-ExecutionPolicy -ExecutionPolicy 'Unrestricted' -Scope 'Process' }; ./install-azd.ps1 -BaseUrl '%1' -Version '%2' -Verbose;"
set "PATH=%PATH%;%LOCALAPPDATA%\Programs\Azure Dev CLI\"
azd version
