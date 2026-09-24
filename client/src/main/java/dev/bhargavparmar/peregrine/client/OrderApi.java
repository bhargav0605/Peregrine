package dev.bhargavparmar.peregrine.client;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import java.io.IOException;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicInteger;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import quickfix.field.Side;

/**
 * An HTTP surface for submitting orders one at a time, in place of
 * OrderSender's fixed-interval loop.
 *
 * <p>This exists for manual control during eBPF work: firing exactly one order
 * at a moment you choose is what lets a kernel trace be lined up against it.
 * A timer loop cannot give you that.
 */
final class OrderApi {

    private static final Logger log = LoggerFactory.getLogger(OrderApi.class);
    private static final ObjectMapper JSON = new ObjectMapper();

    private final TradingApplication application;
    private final int port;
    private final AtomicInteger sequence = new AtomicInteger();

    private HttpServer server;
    private ExecutorService executor;

    OrderApi(TradingApplication application, int port) {
        this.application = application;
        this.port = port;
    }

    void start() throws IOException {
        server = HttpServer.create(new InetSocketAddress(port), 0);
        executor = Executors.newSingleThreadExecutor();
        server.setExecutor(executor);
        server.createContext("/orders", this::handle);
        server.start();

        log.atInfo()
                .addKeyValue("port", port)
                .log("order API listening; POST /orders to submit one");
    }

    void stop() {
        if (server != null) {
            server.stop(1);
        }
        if (executor != null) {
            executor.shutdown();
        }
    }

    private void handle(HttpExchange exchange) throws IOException {
        try {
            if (!"POST".equals(exchange.getRequestMethod())) {
                respond(exchange, 405, Map.of("error", "use POST"));
                return;
            }
            handleSubmit(exchange);
        } finally {
            exchange.close();
        }
    }

    private void handleSubmit(HttpExchange exchange) throws IOException {
        OrderRequest request;
        try {
            request = JSON.readValue(exchange.getRequestBody(), OrderRequest.class);
        } catch (IOException e) {
            respond(exchange, 400, Map.of("error", "invalid JSON: " + e.getMessage()));
            return;
        }

        String error = request.validate();
        if (error != null) {
            respond(exchange, 400, Map.of("error", error));
            return;
        }

        String clOrdID = OrderSender.clOrdID(sequence.incrementAndGet());
        char side = "BUY".equalsIgnoreCase(request.side) ? Side.BUY : Side.SELL;

        OrderSender.Outcome outcome = OrderSender.submit(
                application, clOrdID, request.symbol, side, request.qty, request.price);

        log.atInfo()
                .addKeyValue("cl_ord_id", clOrdID)
                .addKeyValue("outcome", outcome.toString())
                .log("order API submission");

        switch (outcome) {
            case SENT:
                respond(exchange, 201, Map.of("cl_ord_id", clOrdID));
                break;
            case NOT_LOGGED_ON:
                respond(exchange, 503, Map.of("error", "not logged on to broker"));
                break;
            case REFUSED:
                respond(exchange, 502, Map.of("error", "broker refused the order"));
                break;
        }
    }

    private static void respond(HttpExchange exchange, int status, Map<String, Object> body)
            throws IOException {
        byte[] bytes = JSON.writeValueAsBytes(body);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(status, bytes.length);
        try (OutputStream out = exchange.getResponseBody()) {
            out.write(bytes);
        }
    }

    /** The request body's shape. Public fields: Jackson binds them by name. */
    static final class OrderRequest {
        public String symbol;
        public String side;
        public double qty;
        public double price;

        String validate() {
            if (symbol == null || symbol.isBlank()) {
                return "symbol is required";
            }
            if (side == null || !isBuyOrSell(side)) {
                return "side must be BUY or SELL";
            }
            if (qty <= 0) {
                return "qty must be greater than 0";
            }
            if (price <= 0) {
                return "price must be greater than 0";
            }
            return null;
        }

        private static boolean isBuyOrSell(String side) {
            String upper = side.toUpperCase(Locale.ROOT);
            return "BUY".equals(upper) || "SELL".equals(upper);
        }
    }
}
