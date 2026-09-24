package dev.bhargavparmar.peregrine.client;

import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import dev.bhargavparmar.peregrine.client.OrderApi.OrderRequest;
import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;

class OrderApiRequestTest {

    private static OrderRequest request(String symbol, String side, double qty, double price) {
        OrderRequest r = new OrderRequest();
        r.symbol = symbol;
        r.side = side;
        r.qty = qty;
        r.price = price;
        return r;
    }

    @Test
    @DisplayName("a complete, sane request validates")
    void validRequest() {
        assertNull(request("AAPL", "BUY", 100, 220.50).validate());
        assertNull(request("MSFT", "sell", 50, 412.75).validate(),
                "side should be accepted case-insensitively");
    }

    @Test
    @DisplayName("a missing or blank symbol is rejected")
    void missingSymbol() {
        assertNotNull(request(null, "BUY", 100, 220.50).validate());
        assertNotNull(request("  ", "BUY", 100, 220.50).validate());
    }

    @Test
    @DisplayName("side must be BUY or SELL, nothing else")
    void invalidSide() {
        String error = request("AAPL", "HOLD", 100, 220.50).validate();
        assertNotNull(error);
        assertTrue(error.contains("BUY"), error);
    }

    @Test
    @DisplayName("qty and price must be positive, not merely non-negative")
    void nonPositiveNumbers() {
        assertNotNull(request("AAPL", "BUY", 0, 220.50).validate());
        assertNotNull(request("AAPL", "BUY", -10, 220.50).validate());
        assertNotNull(request("AAPL", "BUY", 100, 0).validate());
        assertNotNull(request("AAPL", "BUY", 100, -5).validate());
    }
}
