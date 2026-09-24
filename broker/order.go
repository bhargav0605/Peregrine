package main

import (
	"github.com/quickfixgo/enum"
	"github.com/quickfixgo/field"
	"github.com/quickfixgo/fix44/executionreport"
	"github.com/quickfixgo/fix44/newordersingle"
	"github.com/quickfixgo/quickfix"
	"github.com/shopspring/decimal"
)

// Decimal places used when encoding quantities and prices.
const (
	qtyScale = 2
	pxScale  = 2
)

// order holds the fields the broker needs from a NewOrderSingle.
type order struct {
	clOrdID string
	symbol  string
	side    enum.Side
	qty     decimal.Decimal
}

// readOrder extracts the required fields. A missing one becomes a reject, so
// the client is told rather than left waiting for a report that never comes.
func readOrder(msg newordersingle.NewOrderSingle) (order, quickfix.MessageRejectError) {
	clOrdID, err := msg.GetClOrdID()
	if err != nil {
		return order{}, err
	}

	symbol, err := msg.GetSymbol()
	if err != nil {
		return order{}, err
	}

	side, err := msg.GetSide()
	if err != nil {
		return order{}, err
	}

	qty, err := msg.GetOrderQty()
	if err != nil {
		return order{}, err
	}

	return order{clOrdID: clOrdID, symbol: symbol, side: side, qty: qty}, nil
}

// acknowledge builds the ExecutionReport accepting an order: 150=0, 39=0,
// nothing filled. The IDs are arguments so the result is deterministic in tests.
//
// Per AGENTS.md §13 this is the first report of a possible stream, not a
// terminal state.
func acknowledge(o order, orderID, execID string) executionreport.ExecutionReport {
	report := executionreport.New(
		field.NewOrderID(orderID),
		field.NewExecID(execID),
		field.NewExecType(enum.ExecType_NEW),
		field.NewOrdStatus(enum.OrdStatus_NEW),
		field.NewSide(o.side),
		field.NewLeavesQty(o.qty, qtyScale),
		field.NewCumQty(decimal.Zero, qtyScale),
		field.NewAvgPx(decimal.Zero, pxScale),
	)
	report.SetClOrdID(o.clOrdID)
	report.SetSymbol(o.symbol)
	return report
}
