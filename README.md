# transport

[![Go Reference](https://pkg.go.dev/badge/github.com/nexssp/transport.svg)](https://pkg.go.dev/github.com/nexssp/transport)
[![CI](https://github.com/nexssp/transport/actions/workflows/ci.yml/badge.svg)](https://github.com/nexssp/transport/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

Universal multi-protocol transport layer for `nexss-kernel` actions.

Universal multi-protocol transport layer for `nexss-kernel` actions.

```text
                  ┌─────────────────────────────────┐
                  │      nexss-kernel actions       │
                  └────────────────┬────────────────┘
                                   │
         ┌─────────────────────────┼─────────────────────────┐
         │                         │                         │
         ▼                         ▼                         ▼
   thttp (REST/SSE)         tcli (CLI Tool)          tbus (Event Bus)
   tworker (Workers)        cron (Scheduler)         taskqueue (Task Queue)
```

## Supported Protocols & Transports

- **`thttp`**: Standard HTTP/REST, Server-Sent Events (SSE), streaming NDJSON, and automatic tag-based binding (`path`, `query`, `header`, `cookie`, `form`).
- **`tcli`**: CLI commands, subcommands, flag parsing (`cli:"flag,f"`), positional arguments, and automated `--help` generation.
- **`tbus`**: Zero-allocation, in-process event bus binding with direct memory routing.
- **`tworker`**: Managed background worker schedules with auto-recovery and isolated request contexts.
- **`cron`**: Precise cron-scheduled jobs using standard cron syntax or duration intervals.
- **`taskqueue`**: In-process task queue with priority lanes (`High`, `Normal`, `Low`), retries, dead-letter queue (DLQ), and panic recovery.
- **`mediator`**: CQRS-style mediator supporting 1:1 typed commands and 1:N event publishing.

## Installation

```sh
go get github.com/nexssp/transport@latest
```

## Quick Start

### 1. HTTP Server
```go
server := thttp.New(":8080")
server.Mount([]action.AnyAction{myAction})
server.Do(ctx, nil)
```

### 2. CLI Tool
```go
cli := tcli.New()
cli.Mount([]action.AnyAction{myAction})
cli.Do(ctx, nil)
```

### 3. Background Workers
```go
workerTransport := tworker.New()
workerTransport.Mount([]action.AnyAction{myAction})
workerTransport.Do(ctx, nil)
```
