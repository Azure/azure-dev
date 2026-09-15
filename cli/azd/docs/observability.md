# Observability

## Tracing

`azd` supports logging trace information to either a file or an OpenTelemetry-compatible HTTP endpoint.
`--trace-log-file` writes a JSON file containing the spans for a command execution. `--trace-log-url` sends spans
to an endpoint using the OTLP HTTP protocol.

Both diagnostic outputs use the same exported resource policy as azd's Application Insights telemetry. The resource
contains only azd-defined application fields and standard OpenTelemetry SDK metadata. Values supplied through
`OTEL_RESOURCE_ATTRIBUTES`, `OTEL_SERVICE_NAME`, or other resource detectors are not included in the exported
resource. Span attributes, including first-party extension `ext.*` usage attributes, are unchanged. The trace-file
JSON is useful for inspecting this policy, but it is an exporter-specific representation and is not byte-identical to
the Application Insights payload.

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