package dev.bhargavparmar.peregrine.client;

import io.opentelemetry.api.trace.Tracer;
import java.io.FileInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.atomic.AtomicBoolean;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import quickfix.ConfigError;
import quickfix.DefaultMessageFactory;
import quickfix.FieldConvertError;
import quickfix.Initiator;
import quickfix.MemoryStoreFactory;
import quickfix.SLF4JLogFactory;
import quickfix.SessionSettings;
import quickfix.SocketInitiator;

/**
 * The trading application Peregrine observes: an ordinary JVM running
 * QuickFIX/J as a FIX initiator against the lab broker.
 */
public final class Main {

    private static final Logger log = LoggerFactory.getLogger(Main.class);

    private Main() {
    }

    public static void main(String[] args) {
        try {
            run(args);
        } catch (IllegalArgumentException e) {
            System.err.println("client: " + e.getMessage());
            System.err.println();
            System.err.println(Options.usage());
            System.exit(2);
        } catch (Exception e) {
            // The JSON logs carry the detail. This goes to stderr in plain text
            // so a failure stays readable when stdout is piped into a parser.
            System.err.println("client: " + e.getMessage());
            System.exit(1);
        }
    }

    private static void run(String[] args) throws Exception {
        Options options = Options.parse(args);
        if (options.help) {
            System.out.println(Options.usage());
            return;
        }

        log.atInfo()
                .addKeyValue("pid", ProcessHandle.current().pid())
                .addKeyValue("config", options.configPath)
                .addKeyValue("orders", options.orders == 0 ? "unlimited" : String.valueOf(options.orders))
                .addKeyValue("interval_ms", options.intervalMillis)
                .log("client starting");

        SessionSettings settings = loadSettings(options.configPath);

        Telemetry telemetry = Telemetry.create();
        Tracer tracer = telemetry.tracer();

        TradingApplication application = new TradingApplication(
                tracer, connectHost(settings), connectPort(settings));

        boolean manualOrders = options.httpPort > 0;
        application.setExpectedOrders(manualOrders ? 0 : options.orders);

        // MemoryStore matches the broker, which resets sequence numbers on
        // logon. A file store here would outlive the broker's memory and the
        // two would disagree after a restart.
        Initiator initiator = new SocketInitiator(
                application,
                new MemoryStoreFactory(),
                settings,
                new SLF4JLogFactory(settings),
                new DefaultMessageFactory());

        initiator.start();

        // --http replaces the timer loop entirely: orders come only from
        // requests, so a single order can be fired at a moment you choose and
        // lined up against a kernel trace.
        OrderSender sender = manualOrders
                ? null
                : new OrderSender(application, options.orders, options.intervalMillis);
        OrderApi api = manualOrders ? new OrderApi(application, options.httpPort) : null;
        if (sender != null) {
            sender.start();
        } else {
            api.start();
        }

        // The hook also runs on a normal exit, so guard it: without this the
        // summary is logged twice on every clean run.
        AtomicBoolean alreadyShutDown = new AtomicBoolean();
        Runnable shutdown = () -> {
            if (!alreadyShutDown.compareAndSet(false, true)) {
                return;
            }
            if (sender != null) {
                sender.stop();
            }
            if (api != null) {
                api.stop();
            }
            application.logSummary();
            application.endUnfinishedSpans();
            initiator.stop();
            telemetry.shutdown();
            log.info("client stopped");
        };

        CountDownLatch signalled = new CountDownLatch(1);
        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            if (alreadyShutDown.get()) {
                return;
            }
            log.info("shutdown signal received");
            shutdown.run();
            signalled.countDown();
        }, "pgrn-shutdown"));

        if (manualOrders || options.orders == 0) {
            signalled.await();
        } else {
            application.finished().await();
        }

        shutdown.run();
    }

    private static SessionSettings loadSettings(String path) throws Exception {
        try (InputStream in = new FileInputStream(path)) {
            return new SessionSettings(in);
        } catch (IOException e) {
            throw new IOException("open config '" + path + "': " + e.getMessage(), e);
        }
    }

    private static String connectHost(SessionSettings settings) throws ConfigError {
        return settings.getString(Initiator.SETTING_SOCKET_CONNECT_HOST);
    }

    private static long connectPort(SessionSettings settings) throws ConfigError, FieldConvertError {
        return settings.getLong(Initiator.SETTING_SOCKET_CONNECT_PORT);
    }

    /** Plain command-line options. Nothing here justifies a parsing library. */
    static final class Options {
        String configPath = "client.cfg";
        int orders = 10;
        long intervalMillis = 1000;
        int httpPort;
        boolean help;

        static Options parse(String[] args) {
            Options options = new Options();

            for (int i = 0; i < args.length; i++) {
                String flag = args[i];
                if ("--help".equals(flag) || "-h".equals(flag)) {
                    options.help = true;
                    return options;
                }
                if (i + 1 >= args.length) {
                    throw new IllegalArgumentException("missing value for " + flag);
                }
                String value = args[++i];

                switch (flag) {
                    case "--config":
                        options.configPath = value;
                        break;
                    case "--orders":
                        options.orders = parseNumber(flag, value);
                        break;
                    case "--interval":
                        options.intervalMillis = parseNumber(flag, value);
                        break;
                    case "--http":
                        options.httpPort = parseNumber(flag, value);
                        break;
                    default:
                        throw new IllegalArgumentException("unknown flag " + flag);
                }
            }

            if (options.orders < 0) {
                throw new IllegalArgumentException("--orders must be 0 or more (0 runs until stopped)");
            }
            if (options.intervalMillis < 0) {
                throw new IllegalArgumentException("--interval must be 0 or more");
            }
            if (options.httpPort < 0) {
                throw new IllegalArgumentException("--http must be a positive port number");
            }
            return options;
        }

        private static int parseNumber(String flag, String value) {
            try {
                return Integer.parseInt(value);
            } catch (NumberFormatException e) {
                throw new IllegalArgumentException(flag + " expects a number, got '" + value + "'");
            }
        }

        static String usage() {
            return String.join(System.lineSeparator(),
                    "Usage: java -jar client.jar [options]",
                    "",
                    "  --config <path>    FIX session settings (default client.cfg)",
                    "  --orders <n>       orders to send; 0 runs until stopped (default 10)",
                    "  --interval <ms>    delay between orders (default 1000)",
                    "  --http <port>      accept orders via POST /orders instead of sending",
                    "                     automatically; --orders/--interval are ignored",
                    "  -h, --help         this message",
                    "",
                    "Show the raw FIX conversation with:  -Dfix.log.level=INFO");
        }
    }
}
