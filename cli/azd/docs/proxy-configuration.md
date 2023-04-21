# Proxy Configuration

If users are behind a proxy server then can configure the `HTTP_PROXY` and `HTTPS_PROXY` environment variables that `azd` will use for all http/https requests.

The following examples illustrate using [Telerik Fiddler](https://www.telerik.com/fiddler) as a local proxy server.
After setting the below environment variables you will start seeing requests within the fiddler trace output

Setting the environment variables to invalid values will result in various HTTP related error messages when running `azd` commands.

## Windows

```powershell
$env:HTTP_PROXY = 127.0.0.1:8888
$env:HTTPS_PROXY = 127.0.0.1:8888
```

## Linux / Mac OS

```bash
export HTTP_PROXY=127.0.0.1:8888
export HTTPS_PROXY=127.0.0.1:8888
```

## References

- [Go http package docs](https://pkg.go.dev/net/http)
- [StackOverflow](https://stackoverflow.com/questions/14661511/setting-up-proxy-for-http-client)

Per Go `net/http` package docs

> DefaultTransport is the default implementation of Transport and is used by DefaultClient. It establishes network connections as needed and caches them for reuse by subsequent calls. It uses HTTP proxies as directed by the environment variables HTTP_PROXY, HTTPS_PROXY and NO_PROXY (or the lowercase versions thereof).
