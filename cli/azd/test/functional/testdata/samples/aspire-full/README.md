# AspireAzdTests

## Preview builds

To install preview builds, ensure the internal NuGet `dotnet8` feed is added to your machine:

`dotnet nuget add source --name dotnet8 https://pkgs.dev.azure.com/dnceng/public/_packaging/dotnet8/nuget/v3/index.json`

See instructions in [dotnet/aspire](https://github.com/dotnet/aspire/blob/main/docs/using-latest-daily.md).

## Package versions

The available versions for `AspireVersion` in [Directory.Build.props](./Directory.Build.props) can be found on the NuGet [feed](https://dev.azure.com/dnceng/public/_artifacts/feed/dotnet8/NuGet/Aspire.Hosting/versions).
