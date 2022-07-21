powershell -ex Bypass -c "./install-azd.ps1 -BaseUrl '%1' -Version '' -Verbose"

REM In DevOps prepend the azd CLI location to DevOps' view of %PATH% so azd is
REM accessable in subsequent steps.
echo ##vso[task.prependpath]%LOCALAPPDATA%\Programs\Azure Dev CLI