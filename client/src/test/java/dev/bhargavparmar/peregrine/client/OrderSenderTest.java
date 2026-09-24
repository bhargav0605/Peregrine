package dev.bhargavparmar.peregrine.client;

import static org.junit.jupiter.api.Assertions.assertEquals;

import org.junit.jupiter.api.DisplayName;
import org.junit.jupiter.api.Test;
import quickfix.FieldNotFound;
import quickfix.field.Side;
import quickfix.fix44.NewOrderSingle;

class OrderSenderTest {

    @Test
    @DisplayName("ClOrdID is zero-padded so orders sort and read predictably")
    void clOrdIDFormat() {
        assertEquals("ORD-000001", OrderSender.clOrdID(1));
        assertEquals("ORD-000042", OrderSender.clOrdID(42));
        assertEquals("ORD-999999", OrderSender.clOrdID(999999));
    }

    @Test
    @DisplayName("the order carries every field the broker needs")
    void orderCarriesRequiredFields() throws FieldNotFound {
        NewOrderSingle order = OrderSender.newOrder("ORD-000007", "MSFT", Side.BUY, 250, 412.75);

        assertEquals("ORD-000007", order.getClOrdID().getValue());
        assertEquals("MSFT", order.getSymbol().getValue());
        assertEquals(Side.BUY, order.getSide().getValue());
        assertEquals(250, order.getOrderQty().getValue());
        assertEquals(412.75, order.getPrice().getValue());
    }

    @Test
    @DisplayName("TransactTime is set, without which the broker rejects the order")
    void orderHasTransactTime() throws FieldNotFound {
        NewOrderSingle order = OrderSender.newOrder("ORD-000001", "AAPL", Side.BUY, 100, 220.50);
        assertEquals(true, order.getTransactTime().getValue() != null);
    }

    @Test
    @DisplayName("the thread name fits Linux's 15-character comm limit")
    void threadNameSurvivesIntoEbpfOutput() {
        assertEquals(true, OrderSender.THREAD_NAME.length() <= 15,
                "comm is truncated at 15 chars, so '" + OrderSender.THREAD_NAME
                        + "' would not match in eBPF output");
    }
}
