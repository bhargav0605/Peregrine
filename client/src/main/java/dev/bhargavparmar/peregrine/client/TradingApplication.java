package dev.bhargavparmar.peregrine.client;

import io.opentelemetry.api.common.AttributeKey;
import io.opentelemetry.api.trace.Span;
import io.opentelemetry.api.trace.StatusCode;
import io.opentelemetry.api.trace.Tracer;
import io.opentelemetry.semconv.NetworkAttributes;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ConcurrentMap;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.atomic.AtomicInteger;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import quickfix.Application;
import quickfix.DoNotSend;
import quickfix.FieldNotFound;
import quickfix.IncorrectDataFormat;
import quickfix.IncorrectTagValue;
import quickfix.Message;
import quickfix.MessageCracker;
import quickfix.RejectLogon;
import quickfix.SessionID;
import quickfix.UnsupportedMessageType;
import quickfix.fix44.ExecutionReport;

/**
 * FIX session callbacks and application-level latency measurement.
 *
 * <p>Latency here is what the application itself can see: the moment it handed
 * an order to the FIX engine, to the moment a matching ExecutionReport came
 * back. That total contains our own overhead, the network, and whatever the
 * broker did. Taking it apart is Peregrine's job, not this class's.
 */
public class TradingApplication extends MessageCracker implements Application {

    private static final Logger log = LoggerFactory.getLogger(TradingApplication.class);

    // Not in a shared constants class: these exist only to name attributes
    // consistently between recordSent and onMessage.
    private static final AttributeKey<String> FIX_CL_ORD_ID =
            AttributeKey.stringKey("fix.cl_ord_id");
    private static final AttributeKey<String> FIX_MSG_TYPE =
            AttributeKey.stringKey("fix.msg_type");
    private static final AttributeKey<String> FIX_SESSION_SENDER =
            AttributeKey.stringKey("fix.session.sender");
    private static final AttributeKey<String> FIX_SESSION_TARGET =
            AttributeKey.stringKey("fix.session.target");
    private static final AttributeKey<String> FIX_EXEC_TYPE =
            AttributeKey.stringKey("fix.exec_type");

    /** Timestamps and the open span for an order handed to the engine. */
    static final class Stamp {
        final long monoNanos;
        final Instant wall;
        final Span span;

        Stamp(long monoNanos, Instant wall, Span span) {
            this.monoNanos = monoNanos;
            this.wall = wall;
            this.span = span;
        }
    }

    private final ConcurrentMap<String, Stamp> inFlight = new ConcurrentHashMap<>();
    private final List<Long> latenciesNanos = Collections.synchronizedList(new ArrayList<>());

    private final AtomicInteger sent = new AtomicInteger();
    private final AtomicInteger acknowledged = new AtomicInteger();
    private final AtomicInteger logons = new AtomicInteger();

    private final CountDownLatch finished = new CountDownLatch(1);

    private final Tracer tracer;
    private final String peerAddress;
    private final long peerPort;

    private volatile SessionID sessionID;
    private volatile boolean loggedOn;
    private volatile int expectedOrders;

    // peerAddress/peerPort are the configured broker host (CLAUDE.md §11
    // network.peer.* attributes), not the live socket's remote address.
    TradingApplication(Tracer tracer, String peerAddress, long peerPort) {
        this.tracer = tracer;
        this.peerAddress = peerAddress;
        this.peerPort = peerPort;
    }

    public SessionID sessionID() {
        return sessionID;
    }

    public boolean isLoggedOn() {
        return loggedOn && sessionID != null;
    }

    public void setExpectedOrders(int expectedOrders) {
        this.expectedOrders = expectedOrders;
    }

    /** Releases once every expected order has been acknowledged. */
    public CountDownLatch finished() {
        return finished;
    }

    // ── Application ──────────────────────────────────────────────────────────

    @Override
    public void onCreate(SessionID sessionID) {
        this.sessionID = sessionID;
        log.atInfo().addKeyValue("session", sessionID.toString()).log("session created");
    }

    @Override
    public void onLogon(SessionID sessionID) {
        this.sessionID = sessionID;
        this.loggedOn = true;
        log.atInfo()
                .addKeyValue("session", sessionID.toString())
                .addKeyValue("sender", sessionID.getSenderCompID())
                .addKeyValue("target", sessionID.getTargetCompID())
                .addKeyValue("logon_count", logons.incrementAndGet())
                .log("session logged on");
    }

    @Override
    public void onLogout(SessionID sessionID) {
        this.loggedOn = false;
        log.atInfo()
                .addKeyValue("session", sessionID.toString())
                .addKeyValue("sent", sent.get())
                .addKeyValue("acked", acknowledged.get())
                .addKeyValue("in_flight", inFlight.size())
                .log("session logged out");
    }

    @Override
    public void toAdmin(Message message, SessionID sessionID) {
    }

    @Override
    public void fromAdmin(Message message, SessionID sessionID)
            throws FieldNotFound, IncorrectDataFormat, IncorrectTagValue, RejectLogon {
    }

    @Override
    public void toApp(Message message, SessionID sessionID) throws DoNotSend {
    }

    @Override
    public void fromApp(Message message, SessionID sessionID)
            throws FieldNotFound, IncorrectDataFormat, IncorrectTagValue, UnsupportedMessageType {
        crack(message, sessionID);
    }

    public void onMessage(ExecutionReport report, SessionID sessionID) throws FieldNotFound {
        long receivedMonoNanos = System.nanoTime();
        Instant receivedWall = Instant.now();

        String clOrdID = report.getClOrdID().getValue();
        Stamp stamp = inFlight.remove(clOrdID);

        if (stamp == null) {
            log.atWarn()
                    .addKeyValue("cl_ord_id", clOrdID)
                    .log("execution report for an order we did not send");
            return;
        }

        long latencyNanos = receivedMonoNanos - stamp.monoNanos;
        latenciesNanos.add(latencyNanos);
        int done = acknowledged.incrementAndGet();

        stamp.span.setAttribute(FIX_EXEC_TYPE, String.valueOf(report.getExecType().getValue()));
        stamp.span.setStatus(StatusCode.OK);
        stamp.span.end();

        log.atInfo()
                .addKeyValue("cl_ord_id", clOrdID)
                .addKeyValue("exec_type", String.valueOf(report.getExecType().getValue()))
                .addKeyValue("ord_status", String.valueOf(report.getOrdStatus().getValue()))
                .addKeyValue("order_id", report.getOrderID().getValue())
                .addKeyValue("exec_id", report.getExecID().getValue())
                .addKeyValue("latency_ms", millis(latencyNanos))
                .addKeyValue("sent_wall", stamp.wall.toString())
                .addKeyValue("recv_wall", receivedWall.toString())
                .addKeyValue("sent_mono_ns", stamp.monoNanos)
                .addKeyValue("recv_mono_ns", receivedMonoNanos)
                .log("execution report received");

        if (expectedOrders > 0 && done >= expectedOrders) {
            finished.countDown();
        }
    }

    // ── Called by OrderSender ────────────────────────────────────────────────

    /**
     * Stamps an order as in flight and starts its span. Deliberately does not
     * log: a send can still fail after this point, and logging "order sent"
     * for a message that never left would make the log disagree with reality.
     *
     * <p>One span per order lifecycle, from NewOrderSingle to ExecutionReport
     * (AGENTS.md §18) — not one per FIX message, which heartbeats would drown.
     */
    public Stamp recordSent(String clOrdID) {
        Span span = tracer.spanBuilder("NewOrderSingle")
                .setAttribute(FIX_CL_ORD_ID, clOrdID)
                .setAttribute(FIX_MSG_TYPE, "D")
                .setAttribute(NetworkAttributes.NETWORK_PEER_ADDRESS, peerAddress)
                .setAttribute(NetworkAttributes.NETWORK_PEER_PORT, peerPort)
                .startSpan();

        SessionID session = sessionID;
        if (session != null) {
            span.setAttribute(FIX_SESSION_SENDER, session.getSenderCompID());
            span.setAttribute(FIX_SESSION_TARGET, session.getTargetCompID());
        }

        Stamp stamp = new Stamp(System.nanoTime(), Instant.now(), span);
        inFlight.put(clOrdID, stamp);
        sent.incrementAndGet();
        return stamp;
    }

    /** Logs an order the engine actually accepted. */
    public void logSent(String clOrdID, String symbol, Stamp stamp) {
        log.atInfo()
                .addKeyValue("cl_ord_id", clOrdID)
                .addKeyValue("symbol", symbol)
                .addKeyValue("sent_wall", stamp.wall.toString())
                .addKeyValue("sent_mono_ns", stamp.monoNanos)
                .addKeyValue("in_flight", inFlight.size())
                .log("order sent");
    }

    /**
     * Undoes recordSent when the engine refused the message. Ends the span
     * with an error status: leaving it open would leak it, since nothing else
     * will ever call end() for a message that never left.
     */
    public void discardSent(String clOrdID) {
        Stamp stamp = inFlight.remove(clOrdID);
        if (stamp != null) {
            stamp.span.setStatus(StatusCode.ERROR, "order not sent");
            stamp.span.end();
        }
        sent.decrementAndGet();
    }

    /**
     * Ends any spans still open at shutdown. An order sent just before
     * shutdown may never see its ExecutionReport, and an unfinished span is a
     * leak in the same way an unfinished Stamp would be.
     */
    public void endUnfinishedSpans() {
        inFlight.forEach((clOrdID, stamp) -> {
            stamp.span.setStatus(StatusCode.ERROR, "unanswered at shutdown");
            stamp.span.end();
        });
    }

    public void senderFinished() {
        if (expectedOrders > 0 && acknowledged.get() >= expectedOrders) {
            finished.countDown();
        }
    }

    // ── Reporting ────────────────────────────────────────────────────────────

    public void logSummary() {
        List<Long> sorted;
        synchronized (latenciesNanos) {
            sorted = new ArrayList<>(latenciesNanos);
        }
        Collections.sort(sorted);

        if (sorted.isEmpty()) {
            log.atInfo()
                    .addKeyValue("sent", sent.get())
                    .addKeyValue("acked", 0)
                    .log("no round trips measured");
            return;
        }

        log.atInfo()
                .addKeyValue("sent", sent.get())
                .addKeyValue("acked", acknowledged.get())
                .addKeyValue("unanswered", inFlight.size())
                .addKeyValue("logons", logons.get())
                .addKeyValue("min_ms", millis(sorted.get(0)))
                .addKeyValue("p50_ms", millis(percentile(sorted, 50)))
                .addKeyValue("max_ms", millis(sorted.get(sorted.size() - 1)))
                .log("latency summary");
    }

    private static long percentile(List<Long> sorted, int p) {
        int index = (int) ((p / 100.0) * sorted.size());
        if (index >= sorted.size()) {
            index = sorted.size() - 1;
        }
        return sorted.get(index);
    }

    private static String millis(long nanos) {
        return String.format("%.2f", Duration.ofNanos(nanos).toNanos() / 1_000_000.0);
    }
}
