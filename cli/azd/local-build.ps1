[Environment]::SetEnvironmentVariable('GOARCH','amd64')
[Environment]::SetEnvironmentVariable('GOOS','darwin')

go build -o .\build\azd-darwin-amd64

[Environment]::SetEnvironmentVariable('GOARCH','amd64')
[Environment]::SetEnvironmentVariable('GOOS','linux')

go build -o .\build\azd-linux-amd64

[Environment]::SetEnvironmentVariable('GOOS','windows')

go build -o .\build\azd-windows-amd64.exe