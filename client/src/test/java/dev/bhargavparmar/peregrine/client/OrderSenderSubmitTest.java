package dev.bhargavparmar.peregrine.client;

import static org.junit.jupiter.api.Assertions.assertEquals;

import io.opentelemetry.sdk.testing.exporter.InMemorySpanExporter;
import io.opentelemetry.sdk.trace.SdkTracerProvider;
import io.opentelemetry.sdk.trace.export.SimpleSpanProcessor;
import org.junit.jupiter.api.Test;
import quickfix.field.Side;

/**
 * OrderApi and OrderSender's own loop both submit through this one method, so
 * its outcomes are tested once here rather than through each caller.
 */
class OrderSenderSubmitTest {

    @Test
    void submitBeforeLogonReturnsNotLoggedOn() {
        SdkTracerProvider tracerProvider = SdkTracerProvider.builder()
                .addSpanProcessor(SimpleSpanProcessor.create(InMemorySpanExporter.create()))
                .build();
        try {
            TradingApplication application =
                    new TradingApplication(tracerProvider.get("test"), "127.0.0.1", 9876);
            // Deliberately no onLogon(): sessionID() stays null.

            OrderSender.Outcome outcome = OrderSender.submit(
                    application, "ORD-000001", "AAPL", Side.BUY, 100, 220.50);

            assertEquals(OrderSender.Outcome.NOT_LOGGED_ON, outcome);
        } finally {
            tracerProvider.shutdown();
        }
    }
}
