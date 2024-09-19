# Observability

## Tracing

`azd` supports logging trace information to either a file or an OpenTelemetry compatible HTTP endpoint. The
`--trace-log-file` can be used to write a JSON file containing all the spans for an command execution. Also,
`--trace-log-url` can be used to provide an endpoint to send spans using the OTLP HTTP protocol.

You can use the Jaeger all in one docker image to run Jaeger locally to collect and inspect traces:

```bash
$ docker run -d --name jaeger \
 -e COLLECTOR_OTLP_ENABLED=true \
 -e JAEGER_DISABLED=true \
 -p 16686:16686 \
 -p 4318:4318 \
 jaegertracing/all-in-one
```

And then pass `--trace-log-url localhost` to a command and view the results in the Jaeger UI served at
[http://localhost:16686/search](http://localhost:16686/search)