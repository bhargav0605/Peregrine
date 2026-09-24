package main

import (
	"strings"
	"testing"
	"time"
)

func TestLookupProfile(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantName  string
		wantDelay time.Duration
		wantErr   bool
	}{
		{"normal", "normal", "normal", 0, false},
		{"slow ack", "slow-ack", "slow-ack", 100 * time.Millisecond, false},
		{"upper case", "SLOW-ACK", "slow-ack", 100 * time.Millisecond, false},
		{"surrounding space", "  normal ", "normal", 0, false},
		{"unknown", "does-not-exist", "", 0, true},
		{"empty", "", "", 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lookupProfile(tc.input)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("lookupProfile(%q) = %+v, want an error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("lookupProfile(%q): %v", tc.input, err)
			}
			if got.name != tc.wantName {
				t.Errorf("name = %q, want %q", got.name, tc.wantName)
			}
			if got.ackDelay != tc.wantDelay {
				t.Errorf("ackDelay = %v, want %v", got.ackDelay, tc.wantDelay)
			}
		})
	}
}

// An unknown profile must name the alternatives: the operator mistyped, and a
// bare "unknown profile" leaves them guessing.
func TestLookupProfileErrorListsAlternatives(t *testing.T) {
	_, err := lookupProfile("slowack")
	if err == nil {
		t.Fatal("expected an error for an unknown profile")
	}

	for _, name := range profileNames() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not mention available profile %q", err, name)
		}
	}
}
