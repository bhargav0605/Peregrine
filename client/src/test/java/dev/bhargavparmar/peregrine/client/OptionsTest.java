package dev.bhargavparmar.peregrine.client;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import dev.bhargavparmar.peregrine.client.Main.Options;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

class OptionsTest {

    @Test
    @DisplayName("defaults are usable with no arguments")
    void defaults() {
        Options options = Options.parse(new String[] {});
        assertEquals("client.cfg", options.configPath);
        assertEquals(10, options.orders);
        assertEquals(1000, options.intervalMillis);
    }

    @Test
    @DisplayName("every flag is honoured")
    void allFlags() {
        Options options = Options.parse(new String[] {
            "--config", "/tmp/x.cfg", "--orders", "42", "--interval", "250"
        });
        assertEquals("/tmp/x.cfg", options.configPath);
        assertEquals(42, options.orders);
        assertEquals(250, options.intervalMillis);
    }

    @Test
    @DisplayName("--orders 0 means run until stopped, and is not an error")
    void zeroOrdersIsUnlimited() {
        assertEquals(0, Options.parse(new String[] {"--orders", "0"}).orders);
    }

    @Test
    @DisplayName("a flag with no value is rejected rather than silently ignored")
    void missingValue() {
        IllegalArgumentException e = assertThrows(IllegalArgumentException.class,
                () -> Options.parse(new String[] {"--orders"}));
        assertTrue(e.getMessage().contains("missing value"), e.getMessage());
    }

    @Test
    @DisplayName("an unknown flag is rejected, so a typo does not run with defaults")
    void unknownFlag() {
        assertThrows(IllegalArgumentException.class,
                () -> Options.parse(new String[] {"--ordrs", "5"}));
    }

    @Test
    @DisplayName("non-numeric values name the offending flag")
    void nonNumeric() {
        IllegalArgumentException e = assertThrows(IllegalArgumentException.class,
                () -> Options.parse(new String[] {"--interval", "soon"}));
        assertTrue(e.getMessage().contains("--interval"), e.getMessage());
    }

    @Test
    @DisplayName("negative counts are rejected")
    void negatives() {
        assertThrows(IllegalArgumentException.class,
                () -> Options.parse(new String[] {"--orders", "-1"}));
        assertThrows(IllegalArgumentException.class,
                () -> Options.parse(new String[] {"--interval", "-5"}));
    }

    @Test
    @DisplayName("--help short-circuits before validation")
    void help() {
        assertTrue(Options.parse(new String[] {"--help"}).help);
        assertTrue(Options.parse(new String[] {"-h"}).help);
    }
}
