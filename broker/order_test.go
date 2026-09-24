package main

import (
	"testing"
	"time"

	"github.com/quickfixgo/enum"
	"github.com/quickfixgo/field"
	"github.com/quickfixgo/fix44/newordersingle"
	"github.com/shopspring/decimal"
)

func completeOrder(clOrdID string) newordersingle.NewOrderSingle {
	msg := newordersingle.New(
		field.NewClOrdID(clOrdID),
		field.NewSide(enum.Side_BUY),
		field.NewTransactTime(time.Now().UTC()),
		field.NewOrdType(enum.OrdType_LIMIT),
	)
	msg.SetSymbol("AAPL")
	msg.SetOrderQty(decimal.NewFromInt(100), qtyScale)
	return msg
}

func TestReadOrderAcceptsCompleteOrder(t *testing.T) {
	got, rejectErr := readOrder(completeOrder("ORD-000001"))
	if rejectErr != nil {
		t.Fatalf("readOrder rejected a complete order: %v", rejectErr)
	}

	if got.clOrdID != "ORD-000001" {
		t.Errorf("clOrdID = %q, want ORD-000001", got.clOrdID)
	}
	if got.symbol != "AAPL" {
		t.Errorf("symbol = %q, want AAPL", got.symbol)
	}
	if got.side != enum.Side_BUY {
		t.Errorf("side = %q, want %q", got.side, enum.Side_BUY)
	}
	if !got.qty.Equal(decimal.NewFromInt(100)) {
		t.Errorf("qty = %s, want 100", got.qty)
	}
}

// A missing required field must produce a reject. Returning a zero order with
// no error would leave the client waiting for a report that never arrives.
func TestReadOrderRejectsMissingRequiredField(t *testing.T) {
	tests := []struct {
		name  string
		build func() newordersingle.NewOrderSingle
	}{
		{
			name: "no symbol",
			build: func() newordersingle.NewOrderSingle {
				msg := newordersingle.New(
					field.NewClOrdID("ORD-000001"),
					field.NewSide(enum.Side_BUY),
					field.NewTransactTime(time.Now().UTC()),
					field.NewOrdType(enum.OrdType_LIMIT),
				)
				msg.SetOrderQty(decimal.NewFromInt(100), qtyScale)
				return msg
			},
		},
		{
			name: "no quantity",
			build: func() newordersingle.NewOrderSingle {
				msg := newordersingle.New(
					field.NewClOrdID("ORD-000001"),
					field.NewSide(enum.Side_BUY),
					field.NewTransactTime(time.Now().UTC()),
					field.NewOrdType(enum.OrdType_LIMIT),
				)
				msg.SetSymbol("AAPL")
				return msg
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, rejectErr := readOrder(tc.build()); rejectErr == nil {
				t.Error("readOrder accepted an order missing a required field")
			}
		})
	}
}

// ClOrdID is the correlation key for an order's whole life (CLAUDE.md §7).
// If the broker fails to echo it, nothing downstream can match a report to
// the order that caused it.
func TestAcknowledgeEchoesClOrdID(t *testing.T) {
	o := order{
		clOrdID: "ORD-000042",
		symbol:  "MSFT",
		side:    enum.Side_SELL,
		qty:     decimal.NewFromInt(250),
	}

	report := acknowledge(o, "BRK-00000001", "EXEC-00000001")

	clOrdID, err := report.GetClOrdID()
	if err != nil {
		t.Fatalf("report has no ClOrdID: %v", err)
	}
	if clOrdID != "ORD-000042" {
		t.Errorf("ClOrdID = %q, want ORD-000042 echoed back", clOrdID)
	}
}

func TestAcknowledgeAcceptsWithoutFilling(t *testing.T) {
	o := order{
		clOrdID: "ORD-000042",
		symbol:  "MSFT",
		side:    enum.Side_SELL,
		qty:     decimal.NewFromInt(250),
	}

	report := acknowledge(o, "BRK-00000001", "EXEC-00000001")

	execType, err := report.GetExecType()
	if err != nil {
		t.Fatalf("no ExecType: %v", err)
	}
	if execType != enum.ExecType_NEW {
		t.Errorf("ExecType(150) = %q, want %q", execType, enum.ExecType_NEW)
	}

	ordStatus, err := report.GetOrdStatus()
	if err != nil {
		t.Fatalf("no OrdStatus: %v", err)
	}
	if ordStatus != enum.OrdStatus_NEW {
		t.Errorf("OrdStatus(39) = %q, want %q", ordStatus, enum.OrdStatus_NEW)
	}

	leaves, err := report.GetLeavesQty()
	if err != nil {
		t.Fatalf("no LeavesQty: %v", err)
	}
	if !leaves.Equal(o.qty) {
		t.Errorf("LeavesQty = %s, want the full order quantity %s", leaves, o.qty)
	}

	cum, err := report.GetCumQty()
	if err != nil {
		t.Fatalf("no CumQty: %v", err)
	}
	if !cum.IsZero() {
		t.Errorf("CumQty = %s, want 0 — an acknowledgement fills nothing", cum)
	}
}

func TestAcknowledgeCarriesOrderDetails(t *testing.T) {
	o := order{
		clOrdID: "ORD-000042",
		symbol:  "MSFT",
		side:    enum.Side_SELL,
		qty:     decimal.NewFromInt(250),
	}

	report := acknowledge(o, "BRK-00000001", "EXEC-00000001")

	if symbol, err := report.GetSymbol(); err != nil || symbol != "MSFT" {
		t.Errorf("Symbol = %q (err %v), want MSFT", symbol, err)
	}
	if side, err := report.GetSide(); err != nil || side != enum.Side_SELL {
		t.Errorf("Side = %q (err %v), want %q", side, err, enum.Side_SELL)
	}
	if orderID, err := report.GetOrderID(); err != nil || orderID != "BRK-00000001" {
		t.Errorf("OrderID = %q (err %v), want BRK-00000001", orderID, err)
	}
	if execID, err := report.GetExecID(); err != nil || execID != "EXEC-00000001" {
		t.Errorf("ExecID = %q (err %v), want EXEC-00000001", execID, err)
	}
}
