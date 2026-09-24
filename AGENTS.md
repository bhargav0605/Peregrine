# AGENTS.md

## Purpose

This file defines how coding agents should work on **Peregrine**.

Peregrine is a production-minded, one-sided FIX observability platform. The observed trading application is Java/QuickFIX/J, Peregrine userspace is primarily Go, kernel instrumentation is eBPF/C, and the remote broker simulator is Go.

Agents must optimize for:

- correctness
- evidence-based analysis
- Linux/eBPF safety
- low overhead
- maintainability
- reproducibility
- clear boundaries between observed and inferred behavior

Read `CLAUDE.md` before making architectural changes.

---

# 1. Primary Constraint

Peregrine observes only infrastructure we control.

Assume:

```text
Java Trading App / QuickFIX/J
             │
             │ observed locally
             ▼
        Linux + eBPF
             │
=============│================
             │ FIX/TCP
             ▼
       Go Broker Simulator
       (black-box remote side)
```

Do not design features that require instrumentation on the broker or exchange side unless explicitly marked as optional lab-only integrations.

When reporting timing beyond the local host boundary, use:

```text
external / unobserved time
```

Do not claim remote processing times.

---

# 2. Agent Priorities

When modifying the codebase, use this order of priorities:

1. correctness
2. safety
3. observability accuracy
4. bounded resource consumption
5. testability
6. simplicity
7. performance
8. feature breadth

Do not sacrifice correctness for a more impressive demo.

---


# 2.1 Fixed Lab Roles

Do not change these roles without an explicit architectural decision.

## Our side

```text
Language: Java
FIX engine: QuickFIX/J
Role: FIX Initiator
Purpose: represent the production trading application we control
```

The Java process is the main target for:

- socket correlation
- TCP tracing
- scheduler tracing
- thread/TID analysis
- future JVM correlation

## Remote side

```text
Language: Go
Role: FIX Acceptor / Broker Simulator
Purpose: represent broker, venue, or exchange infrastructure
```

The Go broker may inject deterministic problems, but its internal telemetry is
not an input to Peregrine diagnostics.

## Peregrine

```text
Go:
agent, loader, correlation, CLI, metrics, exporters

C/eBPF:
minimal kernel programs
```

Do not collapse the Java client and Go broker into the same language just for convenience.

---

# 3. Architecture Boundaries

Keep responsibilities separated.

## eBPF layer

Responsible for:

- socket lifecycle
- TCP events
- retransmissions
- process identity
- thread identity
- scheduler events
- timestamps
- low-level network metadata

Not responsible for:

- full FIX session state
- order state machines
- complicated FIX parsing
- business-level causality
- long-term storage
- Prometheus exposition

---


## Java trading application

Responsible for:

- simulating the production-side dummy trader / OMS / order-router path
- performing small deterministic pre-trade risk checks
- running QuickFIX/J as FIX initiator
- generating orders
- receiving ExecutionReports
- maintaining normal FIX session behavior
- providing a real JVM/process/thread target for observation

The Java application should not contain Peregrine diagnosis logic. Risk logic must remain intentionally small and deterministic; do not evolve it into a production risk engine.

Lab-only structured timestamps may exist for validation, but the kernel
observation pipeline must not depend on them.

---

## Go broker simulator

Responsible for:

- acting as FIX acceptor
- returning ExecutionReports
- maintaining remote FIX session behavior
- injecting deterministic remote-side faults
- recording hidden test ground truth

The broker must not export diagnostic signals into Peregrine during an experiment.

---

## Go agent

Responsible for:

- loading/unloading eBPF programs
- reading ring buffers
- normalizing kernel events
- process/socket correlation
- configuration
- filtering
- health checks

---

## FIX layer

Responsible for:

- streaming FIX parsing
- FIX session metadata
- sequence numbers
- message classification
- order identifiers
- execution report state

---

## Correlation layer

Responsible for linking:

```text
FIX message
↕
process/thread
↕
socket
↕
TCP event
↕
scheduler event
↕
timestamp
```

This is the core intelligence of Peregrine.

---

## Export layer

Responsible for:

- Prometheus
- OpenTelemetry
- structured logs
- optional event storage

Never allow exporter requirements to leak into low-level eBPF event structures unnecessarily.

---

# 3.1 Trading-Lab Scope Rules

The lab exists to create realistic evidence, not to build a complete trading stack.

Allowed initial business components:

```text
dummy trader / scenario driver
OMS / order router
small pre-trade risk module
QuickFIX/J initiator
Go broker simulator
```

Keep risk rules deterministic and testable. Suitable examples:

- max quantity
- max notional
- symbol allow-list
- trading-enabled / kill-switch flag

Do not add portfolio accounting, margin engines, pricing engines, smart order routing, or persistence-heavy OMS features unless they directly support an observability experiment.

If market data is required, use synthetic, delayed, or replayed test data. Peregrine must not depend on live market data to function.

---

# 3.2 Operational Lifecycle Scenarios

SOD/EOD behavior belongs in the lab as scenario orchestration.

Useful SOD checks:

- time synchronization status
- risk-limit load
- FIX connectivity
- Logon success
- sequence-number readiness

Useful EOD checks:

- stop new orders
- identify outstanding orders
- complete lab reconciliation
- Logout
- write test audit summary

Peregrine may observe these events but must not become the source of truth for positions, balances, or books and records.

---

# 4. Development Workflow

Before implementing a feature:

1. identify the observable event
2. identify the required hook
3. define the smallest kernel event payload
4. define the userspace representation
5. define correlation keys
6. define expected failure cases
7. define a reproducible lab scenario
8. write tests
9. implement
10. document limitations

Do not start by adding probes without first defining what question the probe helps answer.

---

# 4.1 Working Agreement

These rules apply to every change, not only to features.

## Phase the work

Break work into small phases. One phase does one coherent thing.

At the end of each phase:

- state what was built
- give the exact commands to verify it, and what to expect
- state what comes next
- stop and wait for review

Do not batch several phases into one delivery. A phase that cannot be
summarised in a short paragraph is too large.

## Create structure only when the code needs it

Do not lay out directories ahead of the code that fills them.

```text
start:  main.go
split:  only when a package earns its own boundary
```

`cmd/` earns its place when a second binary exists. `internal/<pkg>/` earns its
place when the code inside it has a reason to be separate. Empty scaffolding is
a cost, not a head start.

## Readability over cleverness

This project is meant to be understood, not admired. Code is read far more
often than it is written.

Prefer:

- straightforward control flow
- comments that explain why, not what
- obvious names over short ones

Avoid:

- premature abstraction
- reflection without a stated reason
- generic machinery with a single caller

If a piece of code needs narration to be understood, rewrite it simpler.

## Verification belongs to the reviewer

Do not run tests, and do not execute the program to confirm behaviour. End each
phase with a "How to test" section instead: exact commands, what a pass looks
like, and what a failure looks like.

Never present predicted output as observed. If a sample was not actually seen,
label it as expected, or describe the outcome in words rather than inventing a
transcript.

Building and vetting still happen before handover. Code that does not compile is
not a deliverable.

---

## Comments

Comment sparingly. Code needing a paragraph to explain it should be rewritten,
not annotated.

Keep:

- a single line where the reason is not evident from the code itself
- a short package doc saying what the package is for

Drop:

- anything restating what the next line already says
- multi-line explanations of ordinary control flow
- commentary that will silently rot when the code changes

The test is simple: if deleting the comment loses nothing, it was noise.

---

## Error handling is a first-class concern

Not a cleanup pass at the end.

- wrap every error with context: `fmt.Errorf("open config %q: %w", path, err)`
- never silently discard an error
- a failure message should say what to do, not only what failed

Good:

```text
port 9876 is already in use.

    lsof -nP -iTCP:9876 -sTCP:LISTEN             # what is holding it
    kill $(lsof -nP -iTCP:9876 -sTCP:LISTEN -t)  # stop it
```

Weak:

```text
bind: address already in use
```

## Test the essentials

Write tests for business logic and for code whose failure would otherwise be
silent.

Test:

- protocol translation
- correlation and state machines
- concurrency safety
- determinism of injected faults

Do not write tests for struct assignment or trivial accessors merely to raise a
coverage number.

---

# 5. Feature Question Rule

Every feature should answer a concrete operational question.

Good:

```text
Did our host retransmit TCP data during a slow FIX order?
```

Good:

```text
How long was the FIX worker thread runnable before it received CPU time?
```

Good:

```text
Was the FIX TCP connection reset locally or remotely?
```

Weak:

```text
Let's collect all TCP kernel events because they may be useful.
```

Avoid telemetry without a clear investigative purpose.

---

# 6. eBPF Rules

Use `cilium/ebpf` for the primary Go integration.

Prefer:

- tracepoints
- stable kernel interfaces
- CO-RE
- BTF
- ring buffers
- bounded maps

Use kprobes only when a suitable tracepoint or stable mechanism is unavailable.

Document why a kprobe is required.

Every eBPF map must have:

- purpose
- key type
- value type
- maximum entries
- lifecycle
- cleanup behavior

Never introduce an unbounded event stream.

---

# 7. Event Structure Rules

Keep kernel events small.

Example:

```c
struct tcp_event {
    __u64 timestamp_ns;
    __u64 socket_cookie;

    __u32 pid;
    __u32 tid;

    __u32 src_addr;
    __u32 dst_addr;

    __u16 src_port;
    __u16 dst_port;

    __u32 bytes;
    __u8  direction;
    __u8  event_type;
};
```

Do not copy entire packet payloads by default.

If payload inspection is introduced later:

- cap copied bytes
- filter aggressively
- make it opt-in
- redact sensitive tags
- document overhead

---

# 8. Timestamp Rules

Use monotonic kernel timestamps for event ordering whenever possible.

Do not mix clocks casually.

If converting to wall-clock timestamps, centralize the conversion logic.

Correlation code should preserve:

```text
raw monotonic timestamp
wall-clock representation
```

where useful.

Never infer ordering from log-print order.

For compliance-oriented demos, document the host time source, synchronization method, and measured drift separately from monotonic event ordering. Do not claim regulatory compliance merely because timestamps are collected.

---

# 9. Socket Correlation

Do not use the 4-tuple alone as a permanent connection identifier.

Connections may reuse:

```text
source IP
source port
destination IP
destination port
```

Prefer a stable identifier such as a socket cookie where available.

A connection model should include:

```go
type Connection struct {
    SocketID   uint64
    PID        uint32
    Process    string

    LocalAddr  netip.AddrPort
    RemoteAddr netip.AddrPort

    CreatedAt  time.Time
    ClosedAt   *time.Time
}
```

---

# 10. FIX Parsing Rules

FIX uses the SOH delimiter:

```text
\x01
```

Test fixtures may display:

```text
|
```

for readability, but production parsing must operate correctly on SOH-delimited messages.

Do not assume:

```text
one TCP read == one FIX message
```

The parser must handle:

- partial messages
- multiple messages in one buffer
- fragmented messages
- malformed messages
- reconnect boundaries

Validate:

```text
8=BeginString
9=BodyLength
10=CheckSum
```

when appropriate.

---

# 10.1 TLS Rule

Plain FIX/TCP is the default development path through v1 core correlation.

If TLS experiments are added later:

- keep network/kernel telemetry useful even when payload is encrypted
- do not pretend kernel TCP probes can parse encrypted FIX
- place plaintext-aware experiments at a controlled userspace/JVM boundary
- make such probes opt-in and version-aware
- bound copied data strictly
- preserve all sensitive-tag redaction requirements

Do not make TLS decryption or plaintext extraction a prerequisite for basic Peregrine operation.

---

# 11. Sensitive FIX Tags

Treat FIX payloads as potentially sensitive.

Default redaction should consider tags such as:

```text
1    Account
50   SenderSubID
553  Username
554  Password
448  PartyID
453  NoPartyIDs
```

Do not persist raw values unless explicitly required by a controlled test.

Never commit real production FIX messages to the repository.

Use synthetic fixtures.

---

# 12. FIX Session Model

Represent session identity explicitly.

Example:

```go
type SessionID struct {
    BeginString  string
    SenderCompID string
    TargetCompID string
}
```

Maintain network connection mapping separately because FIX sessions may reconnect.

Track:

```text
current connection
previous connections
logon time
logout time
expected inbound sequence
next outbound sequence
heartbeat interval
last inbound message
last outbound message
```

Do not build a complete QuickFIX replacement.

Track only what is required for observability.

---

# 13. Order Correlation

Primary key:

```text
ClOrdID (11)
```

Support related fields:

```text
OrigClOrdID (41)
ExecID (17)
ExecType (150)
OrdStatus (39)
```

A valid order lifecycle may include many execution reports.

Do not represent it as:

```text
NewOrderSingle -> one ExecutionReport -> done
```

Use an event stream.

---

# 14. Latency Semantics

Every latency metric must define its exact boundaries.

Examples:

```text
order_ack_latency:
local observation of 35=D
to
local observation of 35=8,150=0
```

```text
local_send_overhead:
userspace send observation
to
local transmit boundary observation
```

Document whether timestamps represent:

- application call
- syscall entry
- syscall exit
- TCP stack event
- packet transmit
- packet receive
- userspace read
- FIX parse completion

Do not use vague metric names like:

```text
network_latency
exchange_latency
broker_latency
```

unless the boundary is provable.

---

# 15. External / Unobserved Time

When calculating:

```text
total round trip
-
known local outbound time
-
known local inbound time
```

the remainder may be shown as:

```text
external / unobserved time
```

It includes potentially:

- remote processing
- network path
- firewalls
- load balancers
- carrier routing
- broker gateway
- venue infrastructure

Do not attribute it further without evidence.

---

# 16. Scheduler Correlation

Scheduler investigation should initially focus on selected FIX process threads.

Do not trace every thread on the host unless explicitly requested in a lab.

Potential correlation:

```text
sched_wakeup(TID)
      │
      │ runnable delay
      ▼
sched_switch -> TID runs
```

Store aggregates where possible.

High-rate raw scheduler event retention should be bounded.

---

# 17. Prometheus Rules

Prometheus labels must remain low-cardinality.

Allowed examples:

```text
session
broker_alias
direction
msg_type
event_type
process
```

Use caution even with session identifiers if environments have many sessions.

Never label metrics with:

```text
ClOrdID
ExecID
socket cookie
PID if highly dynamic
raw IP where cardinality is large
timestamps
```

---

# 18. OpenTelemetry Rules

Use spans only where they represent meaningful operations.

Potential trace model:

```text
FIX NewOrderSingle
        │
        └── wait for ExecutionReport(New)
```

Kernel events can be attached as:

- span events
- attributes
- related diagnostic records

Do not generate a span for every kernel probe event.

That would create excessive volume and poor semantics.

---

# 19. Logging

Use structured logging.

Emit logs as JSON. Logs are evidence in this project, and evidence that needs a
custom parser is evidence nobody uses. JSON keeps them greppable with `jq`,
filterable by field, and correlatable against the event pipeline.

Log what an operator would need in order to act:

- session lifecycle
- message in and message out
- errors, with enough context to do something about them

Add further fields only when a concrete need appears. Log volume is a cost paid
on every run.

Preferred fields:

```text
event
session
socket_id
pid
tid
remote
msg_type
seq_num
cl_ord_id_hash
```

Never log:

- passwords
- complete FIX payloads by default
- secrets
- account details

---

# 20. Incident Findings

A finding must point back to evidence.

Example:

```json
{
  "summary": "Local scheduling contention correlated with order latency spike",
  "confidence": "high",
  "evidence": [
    "sched-runqueue-182",
    "order-latency-982",
    "tcp-sendq-442"
  ]
}
```

Confidence levels:

```text
LOW
MEDIUM
HIGH
```

Do not expose pseudo-scientific values such as:

```text
93.7% confidence
```

unless a real statistical/model basis exists.

---

# 21. Lab Safety

Destructive commands are allowed only inside the controlled lab.

Examples:

```bash
tc qdisc
kill
stress-ng
iptables
network namespace manipulation
```

Scripts must clearly identify the interface/container/namespace they modify.

Every fault-injection script must have a corresponding cleanup mechanism.

Prefer:

```bash
scripts/reset-lab.sh
```

that can restore a known clean state.

---

# 22. Commit Scope

Keep commits focused.

Good:

```text
feat(ebpf): trace TCP retransmissions
```

```text
feat(fix): add streaming message decoder
```

```text
feat(correlation): map FIX sessions to socket cookies
```

```text
test(lab): add packet-loss scenario
```

Avoid commits combining unrelated:

```text
eBPF probes + UI + README + refactor + Docker
```

unless necessary for one coherent feature.

---

# 23. Documentation Expectations

When introducing a new probe, document:

```text
hook
why it is needed
kernel compatibility
event format
overhead concerns
known limitations
```

When introducing a new latency measurement, document:

```text
start event
end event
clock source
correlation key
what the measurement includes
what it does not include
```

---

# 24. Testing Strategy

## Unit tests

Focus on:

- FIX parser
- session state
- correlation
- metric calculations
- redaction
- event normalization

## Integration tests

Validate:

```text
eBPF event -> Go agent -> normalized event
```

## End-to-end tests

Validate scenarios:

```text
order -> ACK
retransmission
network delay
packet loss
disconnect
reconnect
sequence gap
scheduler contention
```

Do not depend on exact latency values.

Use ranges and event ordering.

---

# 25. Kernel Compatibility

Target modern Linux kernels first.

Document tested versions.

Do not claim universal support.

When kernel structure access is required:

- prefer BTF/CO-RE
- detect missing features
- fail with actionable messages

Example:

```text
Peregrine cannot attach scheduler probe:
required BTF type unavailable on kernel 5.x.y.
```

Do not silently disable important telemetry.

---

# 26. CLI UX

CLI output should prioritize investigation.

Good:

```text
$ peregrine inspect ORD-92841

Order: ORD-92841
Session: TRDR->BRKR

NewOrderSingle             14:03:21.120000
tcp_sendmsg                +0.140 ms
local outbound boundary    +0.310 ms

external / unobserved      +8.420 ms

local inbound boundary     14:03:21.128730
tcp_recvmsg                +0.090 ms
ExecutionReport(New)       +0.210 ms

Total                      8.940 ms

Correlated events:
- no TCP retransmission
- scheduler delay < 0.2 ms
- no connection-state change
```

Avoid decorative output that hides evidence.

---

# 27. Initial Milestones

Agents should work in this order unless explicitly directed otherwise.

## Milestone 1

Working asymmetric FIX lab:

```text
Java QuickFIX/J Initiator
        <- FIX 4.4 ->
Go Broker Simulator / Acceptor
```

The Java process must be the observed side.

## Milestone 2

Go eBPF agent boots and reports basic process/socket events.

## Milestone 3

FIX TCP connection tracking.

## Milestone 4

TCP send/receive event stream.

## Milestone 5

Retransmission visibility.

## Milestone 6

Userspace FIX streaming parser.

## Milestone 7

FIX session -> socket correlation.

## Milestone 8

NewOrderSingle -> ExecutionReport correlation.

## Milestone 9

Order latency breakdown.

## Milestone 10

Scheduler runqueue-delay analysis.

## Milestone 11

Sequence-gap / ResendRequest analysis.

## Milestone 12

Incident timeline engine.

## Milestone 13

Prometheus + Grafana.

## Milestone 14

OpenTelemetry export.

## Milestone 15

Hardening and documented v1.0.

Do not prematurely build later milestones while foundational correlation remains unreliable.

---

# 28. First Implementation Target

When beginning from an empty repository, start with:

```text
lab/
```

Create:

```text
lab/java-trading-client
lab/go-broker
```

The Java application must:

```text
Java
└── QuickFIX/J Initiator
    ├── generate NewOrderSingle
    └── receive ExecutionReport
```

The Go application must:

```text
Go Broker Simulator
└── FIX Acceptor
    ├── accept Logon
    ├── process NewOrderSingle
    ├── send ExecutionReport
    └── inject deterministic test faults
```

Example flow:

```text
Java QuickFIX/J
   │
   │ 35=D,11=ORD-000001
   ▼
Go Broker
   │
   │ hidden test behavior
   │ e.g. sleep 100 ms
   │
   │ 35=8,11=ORD-000001,150=0,39=0
   ▼
Java QuickFIX/J
```

Both applications may log monotonic and wall-clock timestamps for test validation.

However:

```text
Peregrine diagnosis input = Java/local-side evidence only
Broker telemetry         = hidden ground truth only
```

The Go broker's logs, metrics, traces, internal timestamps, and fault settings
must not be consumed by Peregrine when generating a conclusion.

After the Java-to-Go session works reliably, introduce the first eBPF
connection-observation probe on the Java host.

---


# 28.1 Black-Box Validation Rule

The Go broker is a test oracle, not an observability source.

For a remote-delay test:

```text
broker fault:
100 ms intentional delay

Peregrine sees:
only Java host / kernel / FIX-side observations
```

Peregrine must produce its finding first.

Only then may the test harness compare the result with broker ground truth.

Valid assertion:

```text
external/unobserved portion ~= injected remote delay
```

Invalid implementation:

```text
read broker delay configuration
and report it as measured latency
```

For local-fault tests, inject contention on the Java host while keeping the
broker normal and verify that Peregrine identifies the local evidence.

---

# 28.2 Future Security Experiments

Security-oriented eBPF work is allowed only after the observability pipeline is stable.

Examples of acceptable later lab experiments:

- detect an unexpected process opening a connection to the FIX endpoint
- detect an unexpected destination or port
- correlate suspicious connection behavior with the existing incident timeline

Do not introduce active XDP/TC/LSM blocking, process termination, or inline policy enforcement into the v1 core. Such work must live behind explicit lab-only boundaries and separate documentation.

---

# 29. Things Agents Must Not Do

Do not:

- claim visibility into remote broker internals
- consume Go broker logs/metrics/traces/fault configuration during diagnosis
- replace the Java QuickFIX/J observed side with Go for convenience
- parse full FIX payloads in eBPF as the first solution
- store full production FIX messages
- export passwords or account identifiers
- add unbounded Prometheus labels
- attach dozens of probes without an investigation goal
- use tcpdump as the core implementation
- depend on BCC for the final architecture
- build a Kubernetes operator before the host agent is mature
- introduce Kafka, ClickHouse, Elasticsearch, or other infrastructure prematurely
- optimize nanoseconds before correlation correctness exists
- create fake causal explanations
- turn the lab into a production OMS, risk engine, or exchange
- require live market data for core tests
- require TLS plaintext extraction for v1
- add active eBPF enforcement before core observability is reliable

---

# 30. Definition of a Good Pull Request

A good PR should answer:

```text
What operational question does this feature answer?
```

It should include:

- implementation
- tests
- reproducible lab instructions
- expected output
- documented limitations

Example:

```text
Feature:
Trace TCP retransmissions for monitored FIX sessions.

Operational question:
Were TCP retransmissions observed locally during a slow order?

Validation:
Inject 1% packet loss with tc netem and verify retransmission
events are associated with the correct session.

Limitation:
A retransmission is correlated with the order timeline but is
not automatically declared the cause of the delay.
```

That is the standard expected throughout Peregrine.

---

# 31. Project North Star

Every agent should keep this question in mind:

> Can an engineer use Peregrine during a real FIX incident to determine what happened on the infrastructure they control, with enough evidence to confidently rule local causes in or out?

If a feature does not move the project toward that outcome, reconsider whether it belongs in the core project.
