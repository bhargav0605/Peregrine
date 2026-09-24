package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Ground-truth event names. Tests match on these strings, so they are part of
// the contract with the validation suite.
const (
	eventLogon          = "logon"
	eventLogout         = "logout"
	eventOrderReceived  = "order_received"
	eventFaultInjected  = "fault_injected"
	eventExecReportSent = "exec_report_sent"
)

// record is one line of ground truth: what the broker really did, and when.
//
// Both clocks are kept on purpose. MonotonicNs is what a test should use for
// durations, because wall clock can step under NTP and would silently corrupt
// a latency assertion. WallClock is for lining events up against other tools.
type record struct {
	Seq         uint64    `json:"seq"`
	WallClock   time.Time `json:"wall_clock"`
	MonotonicNs int64     `json:"monotonic_ns"`
	Event       string    `json:"event"`
	Session     string    `json:"session,omitempty"`
	ClOrdID     string    `json:"cl_ord_id,omitempty"`
	OrderID     string    `json:"order_id,omitempty"`
	ExecID      string    `json:"exec_id,omitempty"`

	// Set only on fault_injected: the profile in force and the delay this
	// order was actually held for. Together they are the answer key.
	Profile         string `json:"profile,omitempty"`
	InjectedDelayNs int64  `json:"injected_delay_ns,omitempty"`
}

// recorder appends ground truth as newline-delimited JSON. Safe for concurrent
// use: orders are handled per session and may overlap.
//
// This file is the answer key for the black-box validation rule (AGENTS.md
// §28.1). Peregrine must never read it while producing a diagnosis; tests open
// it only after forming a conclusion independently.
type recorder struct {
	mu    sync.Mutex
	file  *os.File
	buf   *bufio.Writer
	enc   *json.Encoder
	seq   uint64
	start time.Time
	path  string
}

// newRecorder opens path for appending, creating it 0600: an answer key that is
// pleasant to browse invites exactly the coupling §28.1 forbids.
func newRecorder(path string) (*recorder, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open ground-truth file %q: %w", path, err)
	}

	buf := bufio.NewWriter(file)

	return &recorder{
		file:  file,
		buf:   buf,
		enc:   json.NewEncoder(buf),
		start: time.Now(),
		path:  path,
	}, nil
}

// write stamps the record with a sequence number and both clocks, then appends
// it. A caller that already knows when the event happened should set
// MonotonicNs itself; otherwise it is stamped here.
func (r *recorder) write(rec record) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.seq++
	rec.Seq = r.seq
	rec.WallClock = time.Now()
	if rec.MonotonicNs == 0 {
		rec.MonotonicNs = int64(time.Since(r.start))
	}

	if err := r.enc.Encode(&rec); err != nil {
		return fmt.Errorf("encode ground-truth record: %w", err)
	}

	// Flushed per record: the file is small, and a crash mid-scenario must not
	// lose the very events a test is about to assert on.
	if err := r.buf.Flush(); err != nil {
		return fmt.Errorf("flush ground-truth record: %w", err)
	}

	return nil
}

// since returns monotonic nanoseconds elapsed since the recorder was created,
// so an event can be stamped when it happens rather than when it reaches disk.
func (r *recorder) since() int64 {
	return int64(time.Since(r.start))
}

func (r *recorder) close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.buf.Flush(); err != nil {
		r.file.Close()
		return fmt.Errorf("flush ground truth: %w", err)
	}

	if err := r.file.Close(); err != nil {
		return fmt.Errorf("close ground truth %q: %w", r.path, err)
	}

	return nil
}
