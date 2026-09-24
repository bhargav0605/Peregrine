package dev.bhargavparmar.peregrine.client;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import io.opentelemetry.api.common.AttributeKey;
import io.opentelemetry.api.trace.StatusCode;
import io.opentelemetry.sdk.testing.exporter.InMemorySpanExporter;
import io.opentelemetry.sdk.trace.SdkTracerProvider;
import io.opentelemetry.sdk.trace.data.SpanData;
import io.opentelemetry.sdk.trace.export.SimpleSpanProcessor;
import io.opentelemetry.semconv.NetworkAttributes;
import java.util.List;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;
import quickfix.FieldNotFound;
import quickfix.SessionID;
import quickfix.field.AvgPx;
import quickfix.field.ClOrdID;
import quickfix.field.CumQty;
import quickfix.field.ExecID;
import quickfix.field.ExecType;
import quickfix.field.LeavesQty;
import quickfix.field.OrdStatus;
import quickfix.field.OrderID;
import quickfix.field.Side;
import quickfix.fix44.ExecutionReport;

/**
 * Verifies the span lifecycle: exactly one span per order, ending on report,
 * refusal, or shutdown — never left open, never ended twice.
 */
class TradingApplicationTest {

    private static final SessionID SESSION =
            new SessionID("FIX.4.4", "TRDR", "BRKR");

    private InMemorySpanExporter exporter;
    private SdkTracerProvider tracerProvider;
    private TradingApplication application;

    @BeforeEach
    void setUp() {
        exporter = InMemorySpanExporter.create();
        tracerProvider = SdkTracerProvider.builder()
                .addSpanProcessor(SimpleSpanProcessor.create(exporter))
                .build();
        application = new TradingApplication(
                tracerProvider.get("test"), "127.0.0.1", 9876);
        application.onCreate(SESSION);
        application.onLogon(SESSION);
    }

    @AfterEach
    void tearDown() {
        tracerProvider.shutdown();
    }

    private static ExecutionReport report(String clOrdID, char execType, char ordStatus) {
        ExecutionReport report = new ExecutionReport(
                new OrderID("BRK-1"), new ExecID("EXEC-1"),
                new ExecType(execType), new OrdStatus(ordStatus),
                new Side(Side.BUY), new LeavesQty(0),
                new CumQty(100), new AvgPx(0));
        report.set(new ClOrdID(clOrdID));
        return report;
    }

    @Test
    @DisplayName("a successful round trip produces exactly one span, closed OK")
    void successfulOrderProducesOneOkSpan() throws FieldNotFound {
        application.recordSent("ORD-000001");
        application.onMessage(report("ORD-000001", ExecType.NEW, OrdStatus.NEW), SESSION);

        List<SpanData> spans = exporter.getFinishedSpanItems();
        assertEquals(1, spans.size());

        SpanData span = spans.get(0);
        assertEquals("NewOrderSingle", span.getName());
        assertEquals(StatusCode.OK, span.getStatus().getStatusCode());
        assertEquals("ORD-000001", span.getAttributes().get(AttributeKey.stringKey("fix.cl_ord_id")));
        assertEquals("TRDR", span.getAttributes().get(AttributeKey.stringKey("fix.session.sender")));
        assertEquals("BRKR", span.getAttributes().get(AttributeKey.stringKey("fix.session.target")));
        assertEquals("127.0.0.1", span.getAttributes().get(NetworkAttributes.NETWORK_PEER_ADDRESS));
        assertEquals(9876L, span.getAttributes().get(NetworkAttributes.NETWORK_PEER_PORT));
    }

    @Test
    @DisplayName("a refused order ends its span as an error rather than leaking it")
    void refusedOrderEndsSpanAsError() {
        application.recordSent("ORD-000002");
        application.discardSent("ORD-000002");

        List<SpanData> spans = exporter.getFinishedSpanItems();
        assertEquals(1, spans.size());
        assertEquals(StatusCode.ERROR, spans.get(0).getStatus().getStatusCode());
    }

    @Test
    @DisplayName("an order still open at shutdown is closed as an error, not left dangling")
    void unfinishedSpanIsClosedAtShutdown() {
        application.recordSent("ORD-000003");
        assertTrue(exporter.getFinishedSpanItems().isEmpty());

        application.endUnfinishedSpans();

        List<SpanData> spans = exporter.getFinishedSpanItems();
        assertEquals(1, spans.size());
        assertEquals(StatusCode.ERROR, spans.get(0).getStatus().getStatusCode());
    }

    @Test
    @DisplayName("an ExecutionReport for an order we never sent touches no span")
    void unmatchedReportProducesNoSpan() throws FieldNotFound {
        application.onMessage(report("ORD-999999", ExecType.NEW, OrdStatus.NEW), SESSION);
        assertTrue(exporter.getFinishedSpanItems().isEmpty());
    }

    @Test
    @DisplayName("each order gets its own span; they do not share or overwrite one another")
    void concurrentOrdersGetDistinctSpans() throws FieldNotFound {
        application.recordSent("ORD-000010");
        application.recordSent("ORD-000011");
        application.onMessage(report("ORD-000010", ExecType.NEW, OrdStatus.NEW), SESSION);
        application.onMessage(report("ORD-000011", ExecType.NEW, OrdStatus.NEW), SESSION);

        List<SpanData> spans = exporter.getFinishedSpanItems();
        assertEquals(2, spans.size());
        assertEquals(2, spans.stream().map(SpanData::getSpanId).distinct().count());
    }
}
