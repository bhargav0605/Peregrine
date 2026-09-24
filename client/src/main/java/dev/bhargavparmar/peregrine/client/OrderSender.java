package dev.bhargavparmar.peregrine.client;

import java.time.LocalDateTime;
import java.time.ZoneOffset;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import quickfix.Session;
import quickfix.SessionID;
import quickfix.SessionNotFound;
import quickfix.field.ClOrdID;
import quickfix.field.OrdType;
import quickfix.field.OrderQty;
import quickfix.field.Price;
import quickfix.field.Side;
import quickfix.field.Symbol;
import quickfix.field.TransactTime;
import quickfix.fix44.NewOrderSingle;

/**
 * Sends NewOrderSingle messages from a dedicated, deliberately named thread.
 *
 * <p>Linux truncates a thread's comm to 15 characters and the JVM takes comm
 * from the Java thread name, so this name survives intact into eBPF output when
 * the scheduler milestone needs to find the thread that sends orders.
 */
final class OrderSender {

    /** Must stay under Linux's 15-character comm limit. */
    static final String THREAD_NAME = "pgrn-order-tx";

    private static final Logger log = LoggerFactory.getLogger(OrderSender.class);

    private static final String SYMBOL = "AAPL";
    private static final double QUANTITY = 100;
    private static final double PRICE = 220.50;

    private final TradingApplication application;
    private final int orderCount;
    private final long intervalMillis;
    private final Thread thread;

    private volatile boolean running = true;

    OrderSender(TradingApplication application, int orderCount, long intervalMillis) {
        this.application = application;
        this.orderCount = orderCount;
        this.intervalMillis = intervalMillis;
        this.thread = new Thread(this::run, THREAD_NAME);
        this.thread.setDaemon(true);
    }

    void start() {
        thread.start();
    }

    void stop() {
        running = false;
        thread.interrupt();
    }

    /** How a submit attempt ended, so callers can react without re-deriving it. */
    enum Outcome {
        SENT,
        NOT_LOGGED_ON,
        REFUSED
    }

    /** Predictable so one order can be followed by eye through both sides' logs. */
    static String clOrdID(int sequence) {
        return String.format("ORD-%06d", sequence);
    }

    static NewOrderSingle newOrder(String clOrdID, String symbol, char side,
            double quantity, double price) {
        NewOrderSingle order = new NewOrderSingle(
                new ClOrdID(clOrdID),
                new Side(side),
                new TransactTime(LocalDateTime.now(ZoneOffset.UTC)),
                new OrdType(OrdType.LIMIT));
        order.set(new Symbol(symbol));
        order.set(new OrderQty(quantity));
        order.set(new Price(price));
        return order;
    }

    private void run() {
        log.atInfo()
                .addKeyValue("thread", Thread.currentThread().getName())
                .addKeyValue("orders", orderCount == 0 ? "unlimited" : String.valueOf(orderCount))
                .addKeyValue("interval_ms", intervalMillis)
                .log("order sender started");

        if (!awaitLogon()) {
            log.warn("giving up: never logged on to the broker");
            return;
        }

        int sequence = 0;
        while (running && (orderCount == 0 || sequence < orderCount)) {
            sequence++;

            if (!send(clOrdID(sequence))) {
                // Wait for the session to return rather than burning through
                // the remaining order IDs, and retry this one.
                sequence--;
                if (!awaitLogon()) {
                    return;
                }
                continue;
            }

            if (intervalMillis > 0 && running && (orderCount == 0 || sequence < orderCount)) {
                try {
                    Thread.sleep(intervalMillis);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    break;
                }
            }
        }

        log.atInfo().addKeyValue("sent", sequence).log("order sender finished");
        application.senderFinished();
    }

    private boolean send(String clOrdID) {
        return submit(application, clOrdID, SYMBOL, Side.BUY, QUANTITY, PRICE) == Outcome.SENT;
    }

    /**
     * Builds, stamps and sends one order. Shared by the automatic loop above
     * and by OrderApi's HTTP-triggered submissions, so both paths keep the
     * same invariant: an order is logged as sent only once the engine has
     * actually accepted it.
     */
    static Outcome submit(TradingApplication application, String clOrdID, String symbol,
            char side, double quantity, double price) {
        if (!application.isLoggedOn()) {
            return Outcome.NOT_LOGGED_ON;
        }
        SessionID sessionID = application.sessionID();

        NewOrderSingle order = newOrder(clOrdID, symbol, side, quantity, price);

        // Stamped immediately before handing off, so the measurement excludes
        // our own bookkeeping.
        TradingApplication.Stamp stamp = application.recordSent(clOrdID);
        try {
            if (!Session.sendToTarget(order, sessionID)) {
                log.atWarn().addKeyValue("cl_ord_id", clOrdID).log("engine refused order");
                application.discardSent(clOrdID);
                return Outcome.REFUSED;
            }
            // Logged only once the engine accepts it: an order refused above
            // never left, and saying otherwise would make the log disagree
            // with reality.
            application.logSent(clOrdID, symbol, stamp);
            return Outcome.SENT;
        } catch (SessionNotFound e) {
            log.atWarn().addKeyValue("cl_ord_id", clOrdID).log("no session for order");
            application.discardSent(clOrdID);
            return Outcome.REFUSED;
        }
    }

    private boolean awaitLogon() {
        boolean announced = false;
        while (running) {
            if (application.isLoggedOn()) {
                return true;
            }
            if (!announced) {
                log.info("waiting for logon before sending");
                announced = true;
            }
            try {
                Thread.sleep(200);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                return false;
            }
        }
        return false;
    }
}
