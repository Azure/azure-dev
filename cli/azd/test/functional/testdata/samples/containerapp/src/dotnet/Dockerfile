FROM mcr.microsoft.com/dotnet/aspnet:6.0 AS runtime
WORKDIR /app

dotnet publish -c Release -o published
COPY published/ ./
ENTRYPOINT ["dotnet", "webapp.dll"]