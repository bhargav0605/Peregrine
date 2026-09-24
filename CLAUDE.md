# CLAUDE.md

## Project: Peregrine

> **Peregrine — One-Sided FIX Observability with eBPF**

Peregrine is named after the peregrine falcon: fast, precise, and highly observant. The name reflects the project's focus on low-latency trading infrastructure and evidence-driven visibility.

Peregrine is an eBPF-powered observability and incident-analysis platform for **one-sided FIX trading infrastructure**.

The project assumes that we control only our own side of the FIX connection:

```text
                    OUR INFRASTRUCTURE
┌──────────────────────────────────────────────────────┐
│                                                      │
│  Java Trading Application                           │
│  Dummy Trader / OMS / Order Router                   │
│         │                                            │
│         ▼                                            │
│  Risk Checks / Pre-Trade Validation                  │
│         │                                            │
│         ▼                                            │
│  QuickFIX/J FIX Initiator                            │
│  (represents the production-side system we own)      │
│         │                                            │
│         │ TCP                                        │
│         ▼                                            │
│  Linux Kernel                                        │
│  ├─ sockets                                          │
│  ├─ TCP                                              │
│  ├─ scheduler                                        │
│  ├─ network stack                                    │
│  └─ eBPF probes                                      │
│         │                                            │
└─────────┼────────────────────────────────────────────┘
          │
          │ FIX over TCP
          │
========== OBSERVABILITY BOUNDARY =====================
          │
          ▼
┌──────────────────────────────────────────────────────┐
│ Go Broker Simulator                                  │
│ FIX Acceptor + fault injection                       │
│                                                      │
│ Represents Broker / Venue / Exchange infrastructure. │
│ Peregrine does NOT instrument or consume telemetry    │
│ from this side during diagnosis.                     │
└──────────────────────────────────────────────────────┘
```

The primary engineering question is:

> How much can we explain about FIX latency, session instability, sequence gaps, retransmissions, and local infrastructure issues using telemetry collected only from our own Linux host?

---

# 1. Core Product Goal

Peregrine should help an engineer answer:

> "Was this FIX latency or session problem caused by our infrastructure, or did the unexplained delay occur outside our observable boundary?"

The tool must combine:

- FIX protocol awareness
- realistic order lifecycle context (OMS + risk + FIX session)
- eBPF kernel telemetry
- TCP/socket telemetry
- process/runtime metadata
- Linux scheduler telemetry
- time correlation
- OpenTelemetry-compatible telemetry
- Prometheus metrics
- Grafana visualization
- incident timelines

The project must never claim to know what happened inside a remote broker, venue, or exchange.

The lab may contain richer trading behavior so the telemetry has realistic meaning, but Peregrine itself remains an observability product. The lab should include only the minimum business behavior required to exercise real order flows: a dummy trader, OMS/order-router logic, basic pre-trade risk checks, and deterministic broker responses. It is not intended to become a production OMS, risk platform, or exchange.


If a period of time cannot be observed locally, label it clearly as:

```text
external / unobserved time
```

Never label it as:

```text
exchange processing time
broker processing time
venue latency
```

unless direct instrumentation or trusted telemetry from that system exists.

---

# 2. Example Use Case

A FIX `NewOrderSingle` is sent:

```text
35=D
11=ORD-92841
55=AAPL
54=1
38=100
44=220.50
```

Later an `ExecutionReport` is received:

```text
35=8
11=ORD-92841
150=0
39=0
```

Peregrine should eventually be able to produce a timeline such as:

```text
Order: ORD-92841

14:03:21.120000  NewOrderSingle observed
14:03:21.120090  application send()
14:03:21.120140  tcp_sendmsg()
14:03:21.120310  packet transmitted from local host

                  external / unobserved time
                         8.42 ms

14:03:21.128730  response packet observed locally
14:03:21.128820  tcp_recvmsg()
14:03:21.128940  ExecutionReport processed

Total order -> ACK:       8.94 ms
Local outbound overhead:  0.31 ms
Local inbound overhead:   0.21 ms
External/unobserved:      8.42 ms
```

The project should gradually evolve toward this level of correlation.

---

# 3. Design Principles

## 3.1 One-Sided Observability

Assume only our FIX host can be instrumented.

Do not require:

- broker agents
- exchange agents
- remote eBPF programs
- remote tracing
- packet captures on infrastructure we do not own

---

## 3.2 Evidence, Not Guessing

Peregrine should distinguish between:

- observed facts
- correlations
- hypotheses

Example:

Good:

```text
TCP retransmission observed 20 ms before FIX sequence gap.
```

Bad:

```text
TCP retransmission caused the FIX sequence gap.
```

Unless causality has been proven, use wording such as:

- correlated with
- observed before
- likely associated with
- possible contributor
- local evidence suggests

---

## 3.3 Keep eBPF Focused

Do not implement complex FIX parsing inside eBPF in early versions.

Preferred split:

```text
                 Kernel
                   │
                   │ eBPF
                   ▼
          Kernel Event Stream
                   │
                   ▼
             Go Agent
                   │
       ┌───────────┴───────────┐
       │                       │
       ▼                       ▼
Kernel Events              FIX Metadata
       │                       │
       └───────────┬───────────┘
                   ▼
           Correlation Engine
```

eBPF should initially focus on:

- sockets
- TCP
- process context
- scheduler
- retransmissions
- connection state
- network events

Userspace should focus on:

- FIX parsing
- FIX session state
- ClOrdID
- MsgType
- MsgSeqNum
- ExecutionReport state
- correlation
- aggregation

---

## 3.4 Low Production Overhead

The final design should aim for:

- bounded memory usage
- bounded event volume
- configurable sampling
- configurable session filters
- configurable PID/process filters
- no blocking application behavior
- no modification of trading logic
- safe probe detach
- graceful degradation

Correctness and safety are more important than feature count.

---

# 4. Technical Stack

Primary languages:

```text
Java    - our production-side trading application simulation
          QuickFIX/J FIX initiator, OMS/order-router behavior,
          application-level FIX timestamps and JVM target

Go      - Peregrine userspace agent, correlation engine, CLI,
          exporters, FIX parsing/normalization where applicable

Go      - black-box broker simulator / FIX acceptor / fault injector

C       - minimal eBPF programs where required
```

The language split is intentional:

```text
Java = system being observed
Go   = Peregrine userspace platform
Go   = remote broker simulator
C    = kernel/eBPF instrumentation
```

Do not rewrite the Java trading side in Go merely to simplify the lab.
The Java/JVM boundary is an important part of the project.

Preferred eBPF userspace library:

```text
github.com/cilium/ebpf
```

Avoid requiring BCC for the main implementation.

BCC may be used only for temporary experiments.

Observability:

```text
OpenTelemetry
Prometheus
Grafana
```

Optional storage for high-cardinality event history:

```text
ClickHouse
```

Do not introduce ClickHouse until the basic event pipeline works.

---

# 4.1 Trading-Lab Scope

The lab should exercise a realistic but intentionally small trading path:

```text
Dummy Trader / Scenario Driver
        │
        ▼
OMS / Order Router
        │
        ▼
Pre-Trade Risk Checks
        │
        ▼
QuickFIX/J Initiator
        │
        │ FIX 4.4 / TCP
        ▼
Go Broker Simulator
```

Initial business flow:

```text
NewOrderSingle
  -> risk approve/reject
  -> send to broker
  -> ExecutionReport(New)
  -> optional PartialFill
  -> optional Fill / Reject
```

The Java side should support a small set of deterministic risk rules, for example:

- maximum order quantity
- maximum notional per order
- symbol allow-list
- optional kill-switch / trading-enabled flag

Risk decisions exist to make the order path realistic and to create useful test scenarios. Peregrine must not infer broker-side behavior from these rules.

Market data, if used, should be delayed, synthetic, or replayed test data. Market data is an input to the lab, not a Peregrine observability dependency.

---

# 4.2 Start-of-Day and End-of-Day Scenarios

The lab should eventually model a small set of operational lifecycle checks.

Start of Day (SOD) may include:

- verify clock synchronization
- load risk limits
- verify broker connectivity
- FIX Logon
- validate sequence-number state
- confirm the session is ready for order flow

End of Day (EOD) may include:

- stop new order generation
- identify outstanding/open orders
- reconcile execution state in the lab
- FIX Logout
- persist or archive test audit artifacts
- emit an end-of-session summary

These are lab workflows and operational scenarios. Peregrine should observe the resulting process, FIX, socket, TCP, and scheduler evidence; it should not become the system of record for positions, balances, or regulatory books and records.

---

# 4.3 Compliance and Audit Demonstrations

Peregrine may be used to demonstrate compliance-oriented engineering concepts, especially precise timestamping and evidence preservation.

Useful lab topics include:

- comparing FIX `SendingTime` with local kernel/application observations
- preserving raw monotonic timestamps alongside wall-clock conversions
- demonstrating host clock synchronization and drift detection
- producing a reconstructable local order/session timeline
- exporting selected audit events to immutable storage in an optional lab integration

Any regulatory claim must be tied to a specific rule and configuration. The project should demonstrate engineering controls, not claim regulatory certification.

---

# 5. Proposed Repository Structure

```text
peregrine/
├── CLAUDE.md
├── AGENTS.md
├── README.md
├── Makefile
├── go.mod
│
├── cmd/
│   ├── peregrine-agent/
│   │   └── main.go
│   ├── peregrine-cli/
│   │   └── main.go
│   └── peregrine-collector/
│       └── main.go
│
├── internal/
│   ├── ebpf/
│   │   ├── loader/
│   │   ├── events/
│   │   └── filters/
│   │
│   ├── fix/
│   │   ├── parser/
│   │   ├── session/
│   │   ├── message/
│   │   └── correlation/
│   │
│   ├── network/
│   │   ├── socket/
│   │   ├── tcp/
│   │   └── flow/
│   │
│   ├── scheduler/
│   │
│   ├── correlation/
│   │
│   ├── incident/
│   │
│   ├── metrics/
│   │
│   ├── telemetry/
│   │
│   └── config/
│
├── bpf/
│   ├── include/
│   ├── tcp/
│   ├── socket/
│   ├── scheduler/
│   └── process/
│
├── pkg/
│   └── api/
│
├── lab/
│   ├── java-trading-client/
│   │   ├── src/
│   │   ├── config/
│   │   └── README.md
│   │
│   ├── go-broker/
│   │   ├── cmd/
│   │   ├── internal/
│   │   ├── config/
│   │   └── README.md
│   │
│   ├── scenarios/
│   │   ├── normal/
│   │   ├── broker-delay/
│   │   ├── disconnect/
│   │   ├── sequence-anomaly/
│   │   └── local-cpu-contention/
│   │
│   └── docker/
│
├── deployments/
│   ├── docker-compose/
│   ├── systemd/
│   ├── grafana/
│   ├── prometheus/
│   └── otel/
│
├── scripts/
│   ├── setup-lab.sh
│   ├── inject-delay.sh
│   ├── inject-loss.sh
│   ├── cpu-stress.sh
│   └── reset-lab.sh
│
├── tests/
│   ├── integration/
│   ├── e2e/
│   └── fixtures/
│
└── docs/
    ├── architecture.md
    ├── event-model.md
    ├── correlation.md
    ├── limitations.md
    └── blog-series.md
```

Do not create empty directories merely to satisfy this structure. Add them as functionality is introduced.

---


# 5.1 Java / JVM Role

Java is not merely a test dependency. It is the primary application target.

The expected production-like path is:

```text
OMS / order logic
      │
      ▼
QuickFIX/J
      │
      ▼
Java thread
      │
      ▼
JVM
      │
      ▼
native socket/syscall boundary
      │
      ▼
Linux TCP stack
      │
      ▼
NIC
```

Peregrine should initially explain the Linux/kernel side of this path.

Later versions may add JVM-aware evidence from:

```text
JFR
JMX
OpenTelemetry Java instrumentation
GC/safepoint telemetry
thread-state telemetry
```

These signals are complementary to eBPF.

Do not use JVM telemetry as a substitute for kernel telemetry.

A future latency breakdown may therefore distinguish:

```text
QuickFIX/J/application queueing
JVM/runtime delay
Linux scheduler delay
kernel/TCP overhead
external / unobserved time
```

The project should be able to demonstrate cases where average CPU utilization
looks healthy but the relevant Java FIX thread experiences runqueue delay.

---

# 6. Phase-Based Implementation

## Phase 0 - FIX Lab

Build a deliberately asymmetric lab that mirrors the production constraint:

```text
                 OUR SIDE

        Java Trading Application
        OMS / Order Generator
                 │
                 ▼
        QuickFIX/J Initiator
                 │
                 │ FIX 4.4 / TCP
                 │
========== OBSERVABILITY BOUNDARY ==========
                 │
                 ▼
        Go Broker Simulator
        FIX Acceptor
        Fault Injection
```

### Java side requirements

The Java application represents the system Peregrine is trying to observe.

It must:

- use QuickFIX/J as FIX initiator
- generate `NewOrderSingle`
- receive `ExecutionReport`
- use predictable `ClOrdID`
- support heartbeat and Logon/Logout
- support reconnect
- expose realistic session sequence behavior
- log monotonic and wall-clock application timestamps
- run as a normal JVM process on Linux
- provide identifiable FIX worker/session threads for later scheduler analysis

The Java side may expose structured FIX metadata in the lab for validation,
but Peregrine's kernel/TCP evidence must remain independently collected.

### Go broker requirements

The Go broker represents infrastructure we do not control in production.

It must:

- listen as a FIX acceptor
- accept the Java QuickFIX/J session
- acknowledge `NewOrderSingle` with `ExecutionReport`
- support heartbeat/session behavior
- maintain FIX sequence numbers
- support deterministic fault injection
- record hidden ground-truth timestamps for tests

Fault scenarios should eventually include:

```text
local risk rejection
normal ACK
slow ACK
jitter
reject
partial fill
full fill
delayed heartbeat
disconnect
reconnect
sequence anomaly
duplicate response
out-of-order response where protocol-valid testing allows it
```

### Black-box validation rule

The Go broker is allowed to know the real injected cause:

```text
"wait 100 ms before sending ExecutionReport"
```

Peregrine must NOT read:

- broker logs
- broker metrics
- broker traces
- broker internal timestamps
- broker fault configuration

during diagnosis.

Broker-side information is used only after the experiment as test ground truth.

Example:

```text
Broker ground truth:
100 ms intentional response delay

Peregrine observation:
local host healthy
external / unobserved time ~= 100 ms

Result:
PASS
```

This separation is mandatory because it proves that the one-sided
observability model actually works.

Do not start deep eBPF implementation before the Java-to-Go FIX lab works reliably.

---

## Phase 1 - Process and TCP Connection Discovery

Build an eBPF agent that identifies:

- PID
- process name
- socket
- local address
- remote address
- local port
- remote port
- TCP state transitions
- connection open
- connection close

Possible hooks:

```text
sock:inet_sock_set_state
tcp_v4_connect
tcp_v6_connect
accept paths as appropriate
```

Output example:

```text
FIX connection detected

PID:       18221
Process:   java
Local:     10.0.1.10:43128
Remote:    10.0.2.30:9876
State:     ESTABLISHED
```

---

## Phase 2 - TCP Send/Receive Visibility

Capture local socket activity.

Explore:

```text
tcp_sendmsg
tcp_recvmsg
```

Record:

- timestamp
- PID/TID
- socket cookie or equivalent stable socket identifier
- byte count
- source/destination tuple
- direction

Do not parse full FIX payload in kernel space at this stage.

---

## Phase 3 - TCP Reliability Signals

Add:

```text
tcp_retransmit_skb
TCP state transitions
RST / FIN events
RTT where safely available
```

Correlate retransmissions with a known FIX session.

Example:

```text
14:21:04.110 retransmission
session=TRDR->BRKR
socket=0x...
```

---

## Phase 4 - FIX Awareness

Add userspace FIX parsing.

Initially parse only fields needed for correlation:

```text
8   BeginString
34  MsgSeqNum
35  MsgType
49  SenderCompID
56  TargetCompID
11  ClOrdID
41  OrigClOrdID
17  ExecID
39  OrdStatus
150 ExecType
52  SendingTime
```

Support at minimum:

```text
A  Logon
5  Logout
0  Heartbeat
1  TestRequest
2  ResendRequest
4  SequenceReset
3  Reject
D  NewOrderSingle
F  OrderCancelRequest
G  OrderCancelReplaceRequest
8  ExecutionReport
```

Use streaming-safe parsing because FIX messages may be:

- split across TCP reads
- coalesced into a single TCP read
- delivered as multiple messages in one buffer

Never assume:

```text
1 recv() == 1 FIX message
```

---

# 7. FIX Correlation Model

Primary correlation identifiers:

```text
FIX session:
SenderCompID + TargetCompID + network connection

order:
ClOrdID

replace/cancel chain:
OrigClOrdID + ClOrdID

execution:
ExecID
```

A single order timeline may contain:

```text
NewOrderSingle
ExecutionReport(New)
ExecutionReport(PartialFill)
ExecutionReport(Fill)
```

Do not assume a single `ExecutionReport` completes an order.

---

# 8. Scheduler Telemetry

Later phases should investigate scheduling delay.

Relevant signals may include:

```text
sched:sched_wakeup
sched:sched_wakeup_new
sched:sched_switch
```

Goal:

Estimate:

```text
thread became runnable
        │
        │ runqueue delay
        ▼
thread actually scheduled
```

This helps identify cases where:

```text
CPU utilization = 45%
```

but the critical FIX thread still experiences significant scheduler latency.

Track by TID where possible.

---

# 9. Incident Correlation

Peregrine should eventually build a unified event timeline.

Example:

```text
INCIDENT: FIX latency spike

14:21:02.030 scheduler latency increased
14:21:02.221 FIX worker runnable delay = 18 ms
14:21:03.108 TCP Send-Q increased
14:21:03.340 retransmission observed
14:21:04.110 order ACK latency = 81 ms
14:21:04.291 order ACK latency = 104 ms
14:21:05.022 TestRequest received
14:21:05.941 heartbeat delayed
```

The correlation engine must retain raw facts separately from derived conclusions.

Recommended model:

```go
type Evidence struct {
    Timestamp  time.Time
    Type       EventType
    SessionID  string
    SocketID   uint64
    PID        uint32
    TID        uint32
    Attributes map[string]any
}

type Finding struct {
    Summary    string
    Confidence Confidence
    EvidenceIDs []string
}
```

Do not hard-code causality in the raw event pipeline.

---

# 10. Metrics

Initial Prometheus metrics may include:

```text
peregrine_fix_session_up
peregrine_fix_messages_total
peregrine_fix_orders_total
peregrine_fix_execution_reports_total

peregrine_fix_order_ack_latency_seconds
peregrine_fix_order_execution_latency_seconds

peregrine_tcp_connections
peregrine_tcp_retransmissions_total
peregrine_tcp_rtt_seconds

peregrine_scheduler_runqueue_latency_seconds

peregrine_fix_resend_requests_total
peregrine_fix_sequence_gaps_total
peregrine_fix_test_requests_total
```

Avoid unbounded labels.

Never use `ClOrdID`, `ExecID`, or raw socket IDs as Prometheus labels.

High-cardinality values belong in:

- traces
- logs
- event storage
- incident records

---

# 11. OpenTelemetry

Peregrine should use OpenTelemetry where it is semantically useful.

Potential span:

```text
FIX NewOrderSingle -> ExecutionReport(New)
```

Possible attributes:

```text
fix.session.sender
fix.session.target
fix.msg_type
fix.cl_ord_id
fix.exec_type
fix.msg_seq_num
network.peer.address
network.peer.port
process.pid
```

Be careful with production privacy and cardinality.

Order IDs should be configurable:

```text
raw
hashed
disabled
```

Do not export sensitive FIX payloads by default.

---

# 11.1 TLS and Encrypted FIX

Plain FIX/TCP should be the initial implementation because it keeps correlation semantics visible while the core event model is being validated.

TLS is a later milestone. Once FIX is encrypted:

```text
QuickFIX/J
   ↓
TLS implementation
   ↓
TCP
   ↓
Linux kernel
```

network- and socket-level eBPF still provide useful timing, connection, retransmission, RTT, and scheduler evidence, but the kernel network path no longer exposes plaintext FIX metadata.

A future TLS-aware experiment may explore carefully scoped userspace probes or JVM/runtime instrumentation at a plaintext boundary. This work must be opt-in, version-aware, privacy-conscious, and must not weaken the default rule that Peregrine avoids broad payload capture.

TLS support must not be required for v1.0.

---

# 12. Security and Privacy Rules

FIX traffic may contain sensitive financial information.

Default behavior:

- do not log full FIX messages
- do not persist account identifiers
- do not persist client PII
- do not persist credentials
- do not persist session passwords
- do not expose tag `553` / username or `554` / password
- allow configurable tag redaction
- prefer metadata extraction over payload retention

Any packet/payload capture capability must be:

- explicitly enabled
- clearly documented
- bounded
- filtered
- disabled by default

---

# 13. Failure Injection Lab

Use controlled scenarios to validate Peregrine.

Examples:

## Network latency

```bash
tc qdisc add dev <iface> root netem delay 20ms
```

## Packet loss

```bash
tc qdisc add dev <iface> root netem loss 1%
```

## Jitter

```bash
tc qdisc add dev <iface> root netem delay 10ms 5ms
```

## CPU contention

Use controlled CPU stress.

## Process kill

Kill the FIX acceptor or initiator and observe:

```text
TCP state transition
disconnect
reconnect
Logon
sequence recovery
```

Do not run destructive tests outside the isolated lab environment.

---

# 14. Testing Requirements

Every major feature should include:

- unit tests where practical
- integration test
- reproducible lab scenario
- expected output

eBPF tests must account for kernel differences.

Avoid tests that require exact nanosecond timing.

Use bounded assertions such as:

```text
event exists
ordering is correct
latency falls within expected tolerance
session mapping is correct
```

---


## Black-Box Ground-Truth Tests

For scenarios generated by `lab/go-broker`, tests should compare:

```text
Peregrine conclusion
vs
broker hidden ground truth
```

Examples:

### Remote delay scenario

```text
Go broker:
inject 100 ms before ACK

Expected Peregrine result:
local send path healthy
no meaningful local scheduler delay
no local retransmission evidence
external / unobserved time dominates total latency
```

### Local contention scenario

```text
Go broker:
normal 2-5 ms ACK

Java host:
CPU contention introduced locally

Expected Peregrine result:
scheduler/runqueue delay correlates with slow order
local contribution dominates or materially contributes
```

The diagnostic pipeline must not read ground-truth data before producing its finding.

---

# 15. Coding Standards

## Go

Use:

```bash
gofmt
go vet
go test ./...
```

Prefer:

- small packages
- explicit interfaces
- typed events
- context cancellation
- structured logging
- errors wrapped with context
- table-driven tests

Avoid:

- global mutable state
- giant manager structs
- reflection unless justified
- unnecessary dependencies

---

## eBPF C

Requirements:

- verifier-friendly
- bounded loops
- minimal stack use
- minimal event payload
- explicit structure alignment
- CO-RE where practical
- use ring buffer where appropriate

Avoid:

- complicated parsing
- large payload copies
- unbounded maps
- assumptions tied to a single kernel version

---

# 16. Performance Philosophy

The goal is not to collect everything.

The goal is to collect enough evidence to answer important questions safely.

Prefer:

```text
socket metadata
timestamps
TCP events
scheduler delays
FIX metadata
```

over:

```text
full packet capture
full FIX body capture
every syscall on the machine
```

Support filtering by:

```text
PID
process name
remote IP
remote port
FIX session
```

---

# 17. User-Facing CLI

Possible commands:

```bash
peregrine sessions
peregrine connections
peregrine orders
peregrine incidents
peregrine inspect <order-id>
peregrine inspect-session <session>
peregrine doctor
```

Example:

```text
$ peregrine sessions

SESSION             STATUS       REMOTE             RTT       RETRANS
TRDR->BRKR          LOGGED_ON    10.1.4.20:9876     1.8ms     0
TRDR->BRK2          LOGGED_ON    10.2.8.31:12001    2.3ms     3
```

Example:

```text
$ peregrine inspect ORD-92841

Order: ORD-92841

NewOrderSingle                     14:03:21.120000
Local tcp_sendmsg                  +0.140 ms
Outbound local boundary            +0.310 ms

External / unobserved              +8.420 ms

Inbound local boundary             14:03:21.128730
Local tcp_recvmsg                  +0.090 ms
ExecutionReport(New)               +0.210 ms

Total                              8.940 ms
```

---

# 18. Blog-Series Alignment

The project should be developed in milestones that can each become a blog post.

Suggested series:

1. Production FIX Is a Black Box - Can eBPF Help?
2. Building a Realistic One-Sided FIX Trading Lab
3. Following a FIX Order Into the Linux Kernel
4. Tracing FIX TCP Connections with eBPF
5. Measuring What Happens Before a Packet Leaves Our Server
6. Can We Tell Whether FIX Latency Is Ours or External?
7. Finding TCP Retransmissions Behind Slow FIX Orders
8. Linux Scheduler Latency: The Trading Problem CPU% Won't Show You
9. Correlating FIX ClOrdID with Kernel Events
10. Investigating FIX Sequence Gaps with eBPF
11. Heartbeats, TestRequests, and Session Failures
12. Recreating Production Problems with tc netem
13. Building an Incident Timeline for FIX
14. Exporting Peregrine Telemetry with OpenTelemetry
15. What eBPF Can and Cannot Tell Us About a Remote Exchange

Code should be structured so each milestone can be tagged as a release.

Example:

```text
v0.1  FIX lab
v0.2  TCP connection observer
v0.3  send/receive telemetry
v0.4  retransmissions
v0.5  FIX awareness
v0.6  order correlation
v0.7  scheduler attribution
v0.8  sequence-gap investigation
v0.9  incident engine
v1.0  OTel + Prometheus + Grafana
```

---

# 19. Future Comparative Work

After the custom Peregrine pipeline is working, the lab may be used to compare the hand-built instrumentation with OpenTelemetry eBPF Instrumentation (OBI) or other established observability approaches.

The goal of such a comparison is to understand:

- what Peregrine collects that generic auto-instrumentation does not
- what generic instrumentation provides more cheaply or safely
- where FIX-specific correlation belongs
- what should remain custom versus standardized

OBI should be treated as a comparison/reference implementation, not as a prerequisite for the core learning path.

---

# 20. Important Non-Goals

Do not turn Peregrine into:

- a FIX engine
- an OMS
- a trading strategy
- an exchange simulator beyond what is required for testing
- a packet sniffer product
- a broker monitoring agent
- a generic Kubernetes observability platform
- a replacement for OpenTelemetry
- a replacement for tcpdump
- a replacement for Wireshark
- an inline enforcement, XDP firewall, or eBPF-LSM security product in v1

Active security experiments (for example, detecting an unexpected process connecting to the FIX endpoint) may live as later lab extensions, but they must not distract from the core observability and incident-analysis goal.

Peregrine is specifically:

> A FIX-aware Linux/eBPF observability and incident-analysis tool for the infrastructure boundary we control.

---

# 21. Development Decision Rule

When choosing between two implementations, prefer the one that:

1. is safer to run on a production Linux host
2. introduces less overhead
3. produces evidence instead of assumptions
4. preserves the one-sided observability model
5. keeps kernel code simple
6. keeps FIX/business logic in userspace
7. can be demonstrated reproducibly in the lab
8. can become a meaningful blog milestone

---

# 22. Definition of Done for v1.0

Peregrine v1.0 should be able to:

- discover a configured FIX TCP session
- observe its TCP lifecycle using eBPF
- observe send/receive activity
- identify retransmissions
- parse relevant FIX metadata in userspace
- correlate NewOrderSingle with ExecutionReport
- calculate order-to-ACK latency
- distinguish measured local overhead from external/unobserved time
- observe scheduler latency for relevant threads
- detect and record sequence-gap/resend events
- generate a chronological incident timeline
- expose low-cardinality Prometheus metrics
- export selected telemetry through OpenTelemetry
- provide Grafana dashboards
- reproduce latency, loss, CPU contention, disconnect, and reconnect scenarios in a lab
- document known limitations explicitly

The quality bar is not "lots of probes."

The quality bar is:

> Can Peregrine help an engineer explain what happened to a FIX session using trustworthy evidence from the infrastructure they actually control?
