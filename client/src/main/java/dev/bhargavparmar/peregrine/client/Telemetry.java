package dev.bhargavparmar.peregrine.client;

import io.opentelemetry.api.trace.Tracer;
import io.opentelemetry.exporter.logging.LoggingSpanExporter;
import io.opentelemetry.sdk.OpenTelemetrySdk;
import io.opentelemetry.sdk.resources.Resource;
import io.opentelemetry.sdk.trace.SdkTracerProvider;
import io.opentelemetry.sdk.trace.export.SimpleSpanProcessor;
import io.opentelemetry.semconv.ServiceAttributes;

/**
 * Wires the OpenTelemetry SDK for this process only; nothing here is global.
 *
 * <p>Spans exist to record what this application saw. Peregrine's kernel
 * evidence is collected independently and must never depend on them
 * (CLAUDE.md §5.1): this is a second, complementary view of the same order,
 * not a substitute for the eBPF one.
 */
final class Telemetry {

    private static final String SERVICE_NAME = "peregrine-client";

    private final OpenTelemetrySdk sdk;

    private Telemetry(OpenTelemetrySdk sdk) {
        this.sdk = sdk;
    }

    /**
     * Console exporter only: it needs no collector to see a span at all.
     * OTLP is a later step, once there is somewhere to send it.
     */
    static Telemetry create() {
        Resource resource = Resource.getDefault().toBuilder()
                .put(ServiceAttributes.SERVICE_NAME, SERVICE_NAME)
                .build();

        // Synchronous export on span end. Fine at lab order rates; a
        // BatchSpanProcessor would matter once volume or OTLP network calls
        // make synchronous export a bottleneck.
        SdkTracerProvider tracerProvider = SdkTracerProvider.builder()
                .setResource(resource)
                .addSpanProcessor(SimpleSpanProcessor.create(LoggingSpanExporter.create()))
                .build();

        OpenTelemetrySdk sdk = OpenTelemetrySdk.builder()
                .setTracerProvider(tracerProvider)
                .build();

        return new Telemetry(sdk);
    }

    Tracer tracer() {
        return sdk.getTracer(SERVICE_NAME);
    }

    /** Flushes and stops the exporter. Must run before the JVM exits. */
    void shutdown() {
        sdk.getSdkTracerProvider().shutdown();
    }
}
