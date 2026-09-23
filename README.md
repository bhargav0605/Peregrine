# Peregrine

**One-sided FIX observability with eBPF.**

Peregrine is an observability and incident-analysis tool for FIX trading
infrastructure, built on a constraint that production actually imposes: you can
instrument your own host, and nothing else.

Named after the peregrine falcon — fast, precise, and highly observant.

---

## The problem

A FIX order goes out. An `ExecutionReport` comes back 94 ms later. Something was
slow.

Was it us?

In production you cannot answer that by asking the venue. You have no agent on
their side, no trace context across the boundary, no packet capture on their
network. All you own is one Linux host — and the usual dashboards on that host
say CPU is at 45% and everything is fine.

Peregrine exists to answer one question from that position:

> Was this FIX latency or session problem caused by our infrastructure, or did
> the unexplained delay happen outside our observable boundary?

## The premise

Everything is built around a hard rule: **only our side is instrumented.**

```
                    OUR INFRASTRUCTURE
 ┌──────────────────────────────────────────────┐
 │  Dummy trader → OMS → pre-trade risk         │
 │              ↓                               │
 │  QuickFIX/J initiator            (Java, JVM) │
 │              ↓                               │
 │  Linux kernel                                │
 │  sockets · TCP · scheduler · eBPF probes     │
 └──────────────┬───────────────────────────────┘
                │
                │  FIX 4.4 over TCP
                │
 ════════ OBSERVABILITY BOUNDARY ════════════════
                │
                ▼
 ┌──────────────────────────────────────────────┐
 │  Broker simulator      (Go, FIX acceptor)    │
 │  deterministic fault injection               │
 │                                              │
 │  A BLACK BOX. Its logs, metrics and internal │
 │  timestamps are never an input to a          │
 │  Peregrine diagnosis.                        │
 └──────────────────────────────────────────────┘
```

Time that cannot be observed locally is reported as **`external / unobserved
time`** — never as "exchange processing time". If we did not measure it, we do
not name it.

## What it should produce

```
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

Total order → ACK:        8.94 ms
Local outbound overhead:  0.31 ms
Local inbound overhead:   0.21 ms
External / unobserved:    8.42 ms
```

The interesting cases are the ones a CPU graph hides: the FIX thread sitting in
the runqueue for 18 ms while the machine looks idle, or a TCP retransmission
20 ms before a sequence gap.

## How it proves itself

The broker knows the real answer. Peregrine is not allowed to see it.

```
Broker ground truth:     100 ms deliberate delay before ACK
Peregrine observation:   local send path healthy
                         no scheduler delay, no retransmissions
                         external / unobserved time ≈ 100 ms
Result:                  PASS
```

Ground truth is written to a file the diagnostic pipeline never reads. Tests
consult it only *after* a conclusion has been formed independently. This is the
whole point: it is the only way to show the one-sided model actually works.

## Lab topology

The lab is deliberately asymmetric, and split across two machines so the
boundary is physical rather than notional:

| | runs where | why |
|---|---|---|
| Java trading client + Peregrine agent | Linux host | eBPF needs a real kernel; this is the observed side |
| Go broker simulator | development machine | represents infrastructure we do not control |

The observed side must be Linux with BTF enabled. The broker runs anywhere.

## Evidence, not guesswork

Findings separate what was measured from what was inferred:

- **Good** — "TCP retransmission observed 20 ms before FIX sequence gap."
- **Bad** — "TCP retransmission caused the FIX sequence gap."

Correlation is reported as correlation. Causality is claimed only when proven.

## Stack

| | |
|---|---|
| Java | the application being observed — QuickFIX/J initiator, OMS, risk checks |
| Go | Peregrine agent, correlation engine, CLI, exporters |
| Go | broker simulator and fault injector |
| C | minimal eBPF programs (`cilium/ebpf`, CO-RE) |
| | OpenTelemetry · Prometheus · Grafana |

The Java/JVM boundary is intentional, not incidental — a real syscall path from
a real runtime is part of what makes the evidence meaningful.

## Roadmap

| | |
|---|---|
| v0.1 | asymmetric FIX lab (Java initiator ↔ Go broker) |
| v0.2 | TCP connection discovery via eBPF |
| v0.3 | send/receive telemetry |
| v0.4 | retransmissions |
| v0.5 | FIX awareness (userspace parsing) |
| v0.6 | order correlation — `ClOrdID` to kernel events |
| v0.7 | scheduler attribution |
| v0.8 | sequence-gap investigation |
| v0.9 | incident timeline engine |
| v1.0 | OpenTelemetry · Prometheus · Grafana |

## Status

**Early.** The lab and agent are being rebuilt from the ground up. Treat
anything here as in progress.

## What this is not

Not a FIX engine, not an OMS, not a trading strategy, not a packet sniffer, and
not a replacement for OpenTelemetry, tcpdump or Wireshark. Not an inline
enforcement or eBPF security product.

Peregrine is specifically: *a FIX-aware Linux/eBPF observability and
incident-analysis tool for the infrastructure boundary we control.*

## Design documents

[`CLAUDE.md`](CLAUDE.md) — product goals, phases, correlation model, metrics,
privacy rules.
[`AGENTS.md`](AGENTS.md) — engineering constraints, architecture boundaries,
milestones, review standards.
