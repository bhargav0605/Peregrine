package main

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quickfixgo/fix44/executionreport"
	"github.com/quickfixgo/quickfix"
)

// testClient is a minimal FIX initiator standing in for the Java trading
// client, so the broker can be exercised over a real socket without Java.
type testClient struct {
	*quickfix.MessageRouter

	logon     chan struct{}
	logonOnce sync.Once
	reports   chan executionreport.ExecutionReport
}

func newTestClient() *testClient {
	c := &testClient{
		MessageRouter: quickfix.NewMessageRouter(),
		logon:         make(chan struct{}),
		reports:       make(chan executionreport.ExecutionReport, 16),
	}
	c.AddRoute(executionreport.Route(c.onExecutionReport))
	return c
}

func (c *testClient) OnCreate(quickfix.SessionID) {}

func (c *testClient) OnLogon(quickfix.SessionID) {
	c.logonOnce.Do(func() { close(c.logon) })
}

func (c *testClient) OnLogout(quickfix.SessionID)                   {}
func (c *testClient) ToAdmin(*quickfix.Message, quickfix.SessionID) {}
func (c *testClient) ToApp(*quickfix.Message, quickfix.SessionID) error {
	return nil
}

func (c *testClient) FromAdmin(*quickfix.Message, quickfix.SessionID) quickfix.MessageRejectError {
	return nil
}

func (c *testClient) FromApp(msg *quickfix.Message, sessionID quickfix.SessionID) quickfix.MessageRejectError {
	return c.Route(msg, sessionID)
}

func (c *testClient) onExecutionReport(report executionreport.ExecutionReport, _ quickfix.SessionID) quickfix.MessageRejectError {
	select {
	case c.reports <- report:
	default: // buffer full: drop rather than block the session read loop
	}
	return nil
}

// freePort reserves an ephemeral port so tests never collide with a broker the
// developer already has running.
func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port
}

func parseSettings(t *testing.T, body string) *quickfix.Settings {
	t.Helper()

	settings, err := quickfix.ParseSettings(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	return settings
}

// labSession is a live broker plus a connected client, and the path to the
// ground truth the broker wrote while they talked.
type labSession struct {
	client    *testClient
	sessionID quickfix.SessionID
	truthPath string
}

// startSession brings up the real broker acceptor and a client initiator, and
// returns once the FIX session is logged on.
func startSession(t *testing.T, profileName string) *labSession {
	t.Helper()

	fault, err := lookupProfile(profileName)
	if err != nil {
		t.Fatalf("lookup profile: %v", err)
	}

	port := freePort(t)
	quiet := slog.New(slog.NewJSONHandler(io.Discard, nil))

	truthPath := filepath.Join(t.TempDir(), "ground-truth.jsonl")
	truth, err := newRecorder(truthPath)
	if err != nil {
		t.Fatalf("create recorder: %v", err)
	}
	t.Cleanup(func() { truth.close() })

	acceptorCfg := fmt.Sprintf(`[DEFAULT]
ConnectionType=acceptor
SocketAcceptHost=127.0.0.1
SocketAcceptPort=%d
StartTime=00:00:00
EndTime=00:00:00
HeartBtInt=30
UseDataDictionary=N
ResetOnLogon=Y

[SESSION]
BeginString=FIX.4.4
SenderCompID=BRKR
TargetCompID=TRDR
`, port)

	acceptor, err := quickfix.NewAcceptor(
		newApplication(quiet, truth, fault),
		quickfix.NewMemoryStoreFactory(),
		parseSettings(t, acceptorCfg),
		quickfix.NewNullLogFactory(),
	)
	if err != nil {
		t.Fatalf("create acceptor: %v", err)
	}
	if err := acceptor.Start(); err != nil {
		t.Fatalf("start acceptor: %v", err)
	}
	t.Cleanup(acceptor.Stop)

	initiatorCfg := fmt.Sprintf(`[DEFAULT]
ConnectionType=initiator
SocketConnectHost=127.0.0.1
SocketConnectPort=%d
StartTime=00:00:00
EndTime=00:00:00
HeartBtInt=30
UseDataDictionary=N
ResetOnLogon=Y
ReconnectInterval=1

[SESSION]
BeginString=FIX.4.4
SenderCompID=TRDR
TargetCompID=BRKR
`, port)

	client := newTestClient()
	initiator, err := quickfix.NewInitiator(
		client,
		quickfix.NewMemoryStoreFactory(),
		parseSettings(t, initiatorCfg),
		quickfix.NewNullLogFactory(),
	)
	if err != nil {
		t.Fatalf("create initiator: %v", err)
	}
	if err := initiator.Start(); err != nil {
		t.Fatalf("start initiator: %v", err)
	}
	t.Cleanup(initiator.Stop)

	select {
	case <-client.logon:
	case <-time.After(20 * time.Second):
		t.Fatal("client never logged on to the broker")
	}

	return &labSession{
		client: client,
		sessionID: quickfix.SessionID{
			BeginString:  "FIX.4.4",
			SenderCompID: "TRDR",
			TargetCompID: "BRKR",
		},
		truthPath: truthPath,
	}
}

// awaitRecord polls for a ground-truth record, because the broker writes
// exec_report_sent after handing the message to the engine: on loopback the
// client can see the report before that line runs.
func awaitRecord(t *testing.T, path, event, clOrdID string) record {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, rec := range readRecords(t, path) {
			if rec.Event == event && rec.ClOrdID == clOrdID {
				return rec
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("no %q record for %s within 10s", event, clOrdID)
	return record{}
}

func awaitReport(t *testing.T, client *testClient) executionreport.ExecutionReport {
	t.Helper()

	select {
	case report := <-client.reports:
		return report
	case <-time.After(20 * time.Second):
		t.Fatal("no ExecutionReport arrived")
		return executionreport.ExecutionReport{}
	}
}

// The whole point of the broker: a client logs on, sends an order, and gets it
// acknowledged over a real socket.
func TestOrderIsAcknowledgedOverAFIXSession(t *testing.T) {
	lab := startSession(t, "normal")

	if err := quickfix.SendToTarget(completeOrder("ORD-000001"), lab.sessionID); err != nil {
		t.Fatalf("send NewOrderSingle: %v", err)
	}

	report := awaitReport(t, lab.client)

	clOrdID, err := report.GetClOrdID()
	if err != nil {
		t.Fatalf("report has no ClOrdID: %v", err)
	}
	if clOrdID != "ORD-000001" {
		t.Errorf("ClOrdID = %q, want ORD-000001", clOrdID)
	}
}

// Each order must get its own report, with IDs that do not collide.
func TestEachOrderGetsItsOwnReport(t *testing.T) {
	lab := startSession(t, "normal")

	const orders = 5
	outstanding := make(map[string]bool, orders)

	for i := 1; i <= orders; i++ {
		clOrdID := fmt.Sprintf("ORD-%06d", i)
		outstanding[clOrdID] = true

		if err := quickfix.SendToTarget(completeOrder(clOrdID), lab.sessionID); err != nil {
			t.Fatalf("send %s: %v", clOrdID, err)
		}
	}

	seenExecIDs := make(map[string]bool, orders)

	for i := 0; i < orders; i++ {
		report := awaitReport(t, lab.client)

		clOrdID, err := report.GetClOrdID()
		if err != nil {
			t.Fatalf("report has no ClOrdID: %v", err)
		}
		if !outstanding[clOrdID] {
			t.Fatalf("unexpected or duplicate report for %q", clOrdID)
		}
		delete(outstanding, clOrdID)

		execID, err := report.GetExecID()
		if err != nil {
			t.Fatalf("report has no ExecID: %v", err)
		}
		if seenExecIDs[execID] {
			t.Errorf("ExecID %q reused across reports", execID)
		}
		seenExecIDs[execID] = true
	}

	if len(outstanding) != 0 {
		t.Errorf("%d orders never acknowledged: %v", len(outstanding), outstanding)
	}
}

// The §28.1 loop in miniature: the client observes only the ExecutionReport,
// and only afterwards does the test open the broker's answer key to confirm
// what really happened on the other side.
func TestSessionIsRecordedAsGroundTruth(t *testing.T) {
	lab := startSession(t, "normal")

	if err := quickfix.SendToTarget(completeOrder("ORD-000077"), lab.sessionID); err != nil {
		t.Fatalf("send NewOrderSingle: %v", err)
	}
	awaitReport(t, lab.client)

	received := awaitRecord(t, lab.truthPath, eventOrderReceived, "ORD-000077")
	sent := awaitRecord(t, lab.truthPath, eventExecReportSent, "ORD-000077")

	if sent.Seq <= received.Seq {
		t.Errorf("exec_report_sent seq %d is not after order_received seq %d",
			sent.Seq, received.Seq)
	}
	if sent.MonotonicNs < received.MonotonicNs {
		t.Errorf("exec_report_sent stamped %d before order_received %d",
			sent.MonotonicNs, received.MonotonicNs)
	}
	if sent.OrderID == "" || sent.ExecID == "" {
		t.Errorf("exec_report_sent missing IDs: order_id=%q exec_id=%q",
			sent.OrderID, sent.ExecID)
	}

	var sawLogon bool
	for _, rec := range readRecords(t, lab.truthPath) {
		if rec.Event == eventLogon {
			sawLogon = true
			break
		}
	}
	if !sawLogon {
		t.Error("logon was never recorded as ground truth")
	}
}

// The §28.1 validation loop: observe latency from the client side only, reach a
// conclusion, and only then open the broker's answer key to check it.
func TestSlowAckMatchesGroundTruth(t *testing.T) {
	lab := startSession(t, "slow-ack")

	start := time.Now()
	if err := quickfix.SendToTarget(completeOrder("ORD-000100"), lab.sessionID); err != nil {
		t.Fatalf("send NewOrderSingle: %v", err)
	}
	awaitReport(t, lab.client)
	observed := time.Since(start)

	// What the client alone can say. Loose bounds on purpose: exact timing is
	// not something a test should depend on (AGENTS.md §24).
	const injected = 100 * time.Millisecond
	if observed < 90*time.Millisecond {
		t.Errorf("round trip %v, expected at least roughly the injected %v", observed, injected)
	}
	if observed > 5*time.Second {
		t.Errorf("round trip %v is implausible over loopback", observed)
	}

	// Only now consult the answer key.
	fault := awaitRecord(t, lab.truthPath, eventFaultInjected, "ORD-000100")
	if fault.Profile != "slow-ack" {
		t.Errorf("recorded profile = %q, want slow-ack", fault.Profile)
	}
	if got := time.Duration(fault.InjectedDelayNs); got != injected {
		t.Errorf("recorded injected delay = %v, want %v", got, injected)
	}
}

// The control case must not claim to have done anything.
func TestNormalInjectsNothing(t *testing.T) {
	lab := startSession(t, "normal")

	if err := quickfix.SendToTarget(completeOrder("ORD-000200"), lab.sessionID); err != nil {
		t.Fatalf("send NewOrderSingle: %v", err)
	}
	awaitReport(t, lab.client)
	awaitRecord(t, lab.truthPath, eventExecReportSent, "ORD-000200")

	for _, rec := range readRecords(t, lab.truthPath) {
		if rec.Event == eventFaultInjected {
			t.Errorf("profile 'normal' recorded a fault_injected record: %+v", rec)
		}
	}
}

// Replies run on their own goroutines, so a held report must not become a
// queue. Five orders at 100ms each take about 100ms concurrently and about
// 500ms if they serialise.
func TestDelaysDoNotSerialise(t *testing.T) {
	lab := startSession(t, "slow-ack")

	const orders = 5

	start := time.Now()
	for i := 1; i <= orders; i++ {
		if err := quickfix.SendToTarget(completeOrder(fmt.Sprintf("ORD-%06d", i)), lab.sessionID); err != nil {
			t.Fatalf("send order %d: %v", i, err)
		}
	}
	for i := 0; i < orders; i++ {
		awaitReport(t, lab.client)
	}
	elapsed := time.Since(start)

	if elapsed > 400*time.Millisecond {
		t.Errorf("%d orders at 100ms took %v; replies appear to have serialised "+
			"(that would be about %v) rather than running concurrently",
			orders, elapsed, orders*100*time.Millisecond)
	}
}
