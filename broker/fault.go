package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// profile is a named, deterministic behaviour the broker applies to every
// order. Peregrine never learns which profile is active: it has to work out
// the effect from its own side (AGENTS.md §28.1).
type profile struct {
	name        string
	description string

	// ackDelay is how long the ExecutionReport is held before sending.
	ackDelay time.Duration
}

var profiles = map[string]profile{
	"normal": {
		name:        "normal",
		description: "Immediate ACK. The control case: latency seen is not ours.",
	},
	"slow-ack": {
		name:        "slow-ack",
		description: "Holds the ExecutionReport 100ms before sending.",
		ackDelay:    100 * time.Millisecond,
	},
}

func lookupProfile(name string) (profile, error) {
	p, ok := profiles[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return profile{}, fmt.Errorf("unknown fault profile %q (available: %s)",
			name, strings.Join(profileNames(), ", "))
	}
	return p, nil
}

func profileNames() []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func describeProfiles() string {
	var out strings.Builder
	for _, name := range profileNames() {
		fmt.Fprintf(&out, "  %-10s %s\n", name, profiles[name].description)
	}
	return out.String()
}
