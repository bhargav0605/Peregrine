package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func readRecords(t *testing.T, path string) []record {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open ground truth %q: %v", path, err)
	}
	defer file.Close()

	var records []record

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}

		var rec record
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("decode %q: %v", scanner.Text(), err)
		}
		records = append(records, rec)
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("scan ground truth: %v", err)
	}
	return records
}

func newTestRecorder(t *testing.T) (*recorder, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "ground-truth.jsonl")
	rec, err := newRecorder(path)
	if err != nil {
		t.Fatalf("create recorder: %v", err)
	}
	return rec, path
}

func TestRecorderRoundTrip(t *testing.T) {
	rec, path := newTestRecorder(t)

	err := rec.write(record{
		Event:   eventOrderReceived,
		Session: "FIX.4.4:BRKR->TRDR",
		ClOrdID: "ORD-000001",
		OrderID: "BRK-00000001",
		ExecID:  "EXEC-00000001",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := rec.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	records := readRecords(t, path)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}

	got := records[0]
	if got.Seq != 1 {
		t.Errorf("Seq = %d, want 1", got.Seq)
	}
	if got.Event != eventOrderReceived {
		t.Errorf("Event = %q, want %q", got.Event, eventOrderReceived)
	}
	if got.ClOrdID != "ORD-000001" {
		t.Errorf("ClOrdID = %q, want ORD-000001", got.ClOrdID)
	}
	if got.WallClock.IsZero() {
		t.Error("WallClock was not stamped")
	}
	if got.MonotonicNs <= 0 {
		t.Errorf("MonotonicNs = %d, want a positive elapsed time", got.MonotonicNs)
	}
}

// A caller that knows when an event happened stamps it itself; write must not
// overwrite that with the time the record reached disk.
func TestRecorderKeepsCallerMonotonic(t *testing.T) {
	rec, path := newTestRecorder(t)

	const stamp = int64(123456789)
	if err := rec.write(record{Event: eventOrderReceived, MonotonicNs: stamp}); err != nil {
		t.Fatalf("write: %v", err)
	}
	rec.close()

	records := readRecords(t, path)
	if records[0].MonotonicNs != stamp {
		t.Errorf("MonotonicNs = %d, want %d preserved", records[0].MonotonicNs, stamp)
	}
}

// Orders from different sessions are handled concurrently and share one
// recorder. Nothing may be lost, interleaved or given a duplicate sequence.
func TestRecorderIsConcurrencySafe(t *testing.T) {
	rec, path := newTestRecorder(t)

	const writers, perWriter = 8, 50

	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWriter; j++ {
				if err := rec.write(record{Event: eventOrderReceived}); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if err := rec.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	records := readRecords(t, path)
	want := writers * perWriter
	if len(records) != want {
		t.Fatalf("got %d records, want %d", len(records), want)
	}

	// Sequence numbers must be exactly 1..N, each appearing once.
	seen := make(map[uint64]bool, want)
	for _, r := range records {
		if r.Seq < 1 || r.Seq > uint64(want) {
			t.Fatalf("Seq %d outside 1..%d", r.Seq, want)
		}
		if seen[r.Seq] {
			t.Fatalf("Seq %d appears more than once", r.Seq)
		}
		seen[r.Seq] = true
	}
}

// The file is an answer key, not something to browse.
func TestRecorderFileIsNotWorldReadable(t *testing.T) {
	rec, path := newTestRecorder(t)
	defer rec.close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}
}

func TestRecorderAppendsAcrossRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ground-truth.jsonl")

	for i := 0; i < 2; i++ {
		rec, err := newRecorder(path)
		if err != nil {
			t.Fatalf("create recorder (run %d): %v", i+1, err)
		}
		if err := rec.write(record{Event: eventLogon}); err != nil {
			t.Fatalf("write (run %d): %v", i+1, err)
		}
		if err := rec.close(); err != nil {
			t.Fatalf("close (run %d): %v", i+1, err)
		}
	}

	if records := readRecords(t, path); len(records) != 2 {
		t.Fatalf("got %d records across two runs, want 2", len(records))
	}
}
