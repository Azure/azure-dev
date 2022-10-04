powershell -ex Bypass -c "./install-azd.ps1 -BaseUrl '%1' -Version '' -Verbose"
if %errorlevel% neq 0 exit /b %errorlevel%

REM Simulate that PATH was updated.
set path=%LOCALAPPDATA%\Programs\Azure Dev CLI;%PATH%
azd version
if %errorlevel% neq 0 exit /b %errorlevel%

powershell -ex Bypass -c "./uninstall-azd.ps1 -Verbose"
if %errorlevel% neq 0 exit /b %errorlevel%

