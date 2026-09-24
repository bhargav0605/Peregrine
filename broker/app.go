package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quickfixgo/fix44/newordersingle"
	"github.com/quickfixgo/quickfix"
)

// maxInFlight bounds concurrently delayed replies. Unbounded goroutines would
// be an unbounded event stream by another name.
const maxInFlight = 256

// application implements quickfix.Application.
type application struct {
	*quickfix.MessageRouter

	log   *slog.Logger
	truth *recorder
	fault profile

	orderSeq atomic.Uint64
	execSeq  atomic.Uint64

	inFlight chan struct{}
	wg       sync.WaitGroup
}

func newApplication(log *slog.Logger, truth *recorder, fault profile) *application {
	a := &application{
		MessageRouter: quickfix.NewMessageRouter(),
		log:           log,
		truth:         truth,
		fault:         fault,
		inFlight:      make(chan struct{}, maxInFlight),
	}
	a.AddRoute(newordersingle.Route(a.onNewOrderSingle))
	return a
}

func (a *application) OnCreate(sessionID quickfix.SessionID) {
	a.log.Info("session created", "session", sessionID.String())
}

func (a *application) OnLogon(sessionID quickfix.SessionID) {
	a.log.Info("session logged on",
		"session", sessionID.String(),
		"sender", sessionID.SenderCompID,
		"target", sessionID.TargetCompID)
	a.record(record{Event: eventLogon, Session: sessionID.String()})
}

func (a *application) OnLogout(sessionID quickfix.SessionID) {
	a.log.Info("session logged out", "session", sessionID.String())
	a.record(record{Event: eventLogout, Session: sessionID.String()})
}

func (a *application) ToAdmin(*quickfix.Message, quickfix.SessionID) {}

func (a *application) ToApp(*quickfix.Message, quickfix.SessionID) error { return nil }

func (a *application) FromAdmin(*quickfix.Message, quickfix.SessionID) quickfix.MessageRejectError {
	return nil
}

// FromApp routes to a handler. Message types without a route are rejected by
// the router, so an unhandled type is refused rather than silently accepted.
func (a *application) FromApp(msg *quickfix.Message, sessionID quickfix.SessionID) quickfix.MessageRejectError {
	return a.Route(msg, sessionID)
}

// record writes ground truth. A failure invalidates later validation, so it is
// logged loudly, but it must not take the session down with it.
func (a *application) record(rec record) {
	if err := a.truth.write(rec); err != nil {
		a.log.Error("ground-truth write failed",
			"event", rec.Event, "error", err.Error())
	}
}

func (a *application) onNewOrderSingle(msg newordersingle.NewOrderSingle, sessionID quickfix.SessionID) quickfix.MessageRejectError {
	receivedAt := a.truth.since()

	o, rejectErr := readOrder(msg)
	if rejectErr != nil {
		a.log.Warn("rejecting order",
			"session", sessionID.String(), "reason", rejectErr.Error())
		return rejectErr
	}

	a.log.Info("order received",
		"cl_ord_id", o.clOrdID,
		"symbol", o.symbol,
		"side", string(o.side),
		"qty", o.qty.String())

	a.record(record{
		Event:       eventOrderReceived,
		MonotonicNs: receivedAt,
		Session:     sessionID.String(),
		ClOrdID:     o.clOrdID,
	})

	a.dispatch(sessionID, o)
	return nil
}

// dispatch answers on a separate goroutine. Sleeping on the session read loop
// would stall heartbeats too, and would turn a per-order delay into a queue:
// order N would appear to take N times the delay.
func (a *application) dispatch(sessionID quickfix.SessionID, o order) {
	select {
	case a.inFlight <- struct{}{}:
	default:
		a.log.Warn("in-flight limit reached, session read loop will block",
			"limit", maxInFlight, "cl_ord_id", o.clOrdID)
		a.inFlight <- struct{}{}
	}

	a.wg.Add(1)
	go func() {
		defer func() {
			<-a.inFlight
			a.wg.Done()
		}()
		a.reply(sessionID, o)
	}()
}

func (a *application) reply(sessionID quickfix.SessionID, o order) {
	if a.fault.ackDelay > 0 {
		a.record(record{
			Event:           eventFaultInjected,
			Session:         sessionID.String(),
			ClOrdID:         o.clOrdID,
			Profile:         a.fault.name,
			InjectedDelayNs: int64(a.fault.ackDelay),
		})
		time.Sleep(a.fault.ackDelay)
	}

	orderID := fmt.Sprintf("BRK-%08d", a.orderSeq.Add(1))
	execID := fmt.Sprintf("EXEC-%08d", a.execSeq.Add(1))

	// A send failure means the session is already gone, so there is nowhere to
	// report the problem to either.
	if err := quickfix.SendToTarget(acknowledge(o, orderID, execID), sessionID); err != nil {
		a.log.Error("send execution report failed",
			"cl_ord_id", o.clOrdID, "error", err.Error())
		return
	}

	a.log.Info("execution report sent",
		"cl_ord_id", o.clOrdID, "order_id", orderID, "exec_id", execID)

	a.record(record{
		Event:   eventExecReportSent,
		Session: sessionID.String(),
		ClOrdID: o.clOrdID,
		OrderID: orderID,
		ExecID:  execID,
	})
}

// drain waits for in-flight replies, so shutdown does not discard reports a
// client is still waiting on.
func (a *application) drain(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("drain in-flight replies: %w", ctx.Err())
	}
}
