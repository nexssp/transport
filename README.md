# Nexss Transport

<small>Nexss Flow 🌀 ready</small>

<!-- project-status -->

[![CI](https://github.com/nexssp/transport/actions/workflows/ci.yml/badge.svg)](https://github.com/nexssp/transport/actions/workflows/ci.yml)
[![Latest tag](https://img.shields.io/github/v/tag/nexssp/transport?sort=semver&label=latest%20tag)](https://github.com/nexssp/transport/tags)
[![Go Reference](https://pkg.go.dev/badge/github.com/nexssp/transport.svg)](https://pkg.go.dev/github.com/nexssp/transport)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

<!-- /project-status -->

Nexss Transport connects [Nexss Kernel](https://github.com/nexssp/kernel) actions to protocol-specific entry points and runtimes. Adapters translate incoming requests, commands, events, and schedules into typed action executions, while the action handler stays independent of the transport.

A common `transport.Transport` contract provides binding discovery, action mounting, and lifecycle management. An action can carry multiple bindings and be mounted on more than one transport.

> **Scope:** This module includes HTTP and CLI adapters, in-process messaging and scheduling tools, and [Nexss Flow](https://github.com/nexssp/flow) integration. Its bus and task queue are in-memory components, not external brokers or durable storage.

## Architecture

```
Application / Nexss Flow
          │
          ▼
Typed Nexss Kernel actions
  .Route(thttp.GET(... )) · .Route(tcli.Command(...)) · other bindings
          │
          ▼
Nexss Transport
  ├── thttp     HTTP routes, SSE, streaming
  ├── tcli      commands and flags
  ├── tbus      in-process event bus
  ├── tmsg      canonical message envelopes
  ├── tworker   interval-driven workers
  └── cron      scheduled actions
          │
          └── shared codecs, identity, bus, mediator, and task queue packages
```

Adapters implement a shared lifecycle: `CanHandle` recognizes their binding types, `Mount` registers actions, and `Do(ctx, ... )` starts a listener or runs the selected command. Long-running transports stop when their context is canceled.

## Install

Requires **Go 1.26.9 or newer**.

```
go get github.com/nexssp/transport@latest
```

## Quick start: HTTP

Create a typed action, bind it to a route, then mount it on an HTTP server:

```go
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport/thttp"
 )

type HelloRequest struct {
	Name string `path:"name"`
}

type HelloResponse struct {
	Message string `json:"message"`
}

func main() {
	hello := action.New("hello", func(_ context.Context, req HelloRequest) (HelloResponse, error) {
		return HelloResponse{Message: "Hello, " + req.Name}, nil
	}).Route(thttp.GET("/hello/{name}" )).Build()

	server := thttp.New(":8080" )
	server.Mount([]action.AnyAction{hello})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if _, err := server.Do(ctx, nil); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
```

Run the application and try the route:

```
curl http://localhost:8080/hello/Maks
```

The HTTP adapter binds the path parameter to `HelloRequest.Name` and encodes the response as JSON.

## Quick start: CLI

The CLI adapter maps actions to named commands, binds request fields to flags or positionals, and generates help from the command metadata:

```go
package main

import (
	"context"
	"log"

	"github.com/nexssp/kernel/action"
	"github.com/nexssp/transport/tcli"
 )

type HelloRequest struct {
	Name string `cli:"name,n"`
}

func main() {
	hello := action.New("hello", func(_ context.Context, req HelloRequest) (string, error) {
		return "Hello, " + req.Name, nil
	}).Route(tcli.Command("hello", "Greet someone")).Build()

	cli := tcli.New() // Reads os.Args by default.
	cli.Mount([]action.AnyAction{hello})
	if _, err := cli.Do(context.Background(), nil); err != nil {
		log.Fatal(err)
	}
}
```

For example, invoke the built application as `app hello --name Julia`. Use `tcli.WithArgs(...)` to supply arguments directly, and `tcli.WithOutput(...)` to redirect output in tests or embedded use.

## Transports and packages

| Package | Purpose |
| --- | --- |
| [`thttp`](thttp/) | HTTP routes and raw handlers; automatic `path`, `query`, `header`, `cookie`, and `form` binding; middleware; configurable codecs and request limits; categorized error-to-status mapping; Server-Sent Events (SSE); and O(1)-memory NDJSON streaming. |
| [`tcli`](tcli/) | CLI command routing, aliases, flags, positional arguments, request schemas, generated help, and encoded results. |
| [`tbus`](tbus/) | Connects action bindings to a concurrent, in-process publish/subscribe bus. |
| [`tmsg`](tmsg/) and [`message`](message/) | Route protocol-neutral message envelopes through an in-process router, preserving request, execution, and trace identity. |
| [`tworker`](tworker/) | Run bound actions on intervals with isolated request contexts and optional restart-on-error behavior. |
| [`cron`](cron/) | Run actions on standard cron schedules or fixed intervals. |
| [`bus`](bus/) | Generic in-process pub/sub, typed 1:1 command routing, and logging, panic-recovery, and timeout middleware. |
| [`bus/mediator`](bus/mediator/) | CQRS-style mediator: typed 1:1 commands and 1:N event publishing. |
| [`bus/taskqueue`](bus/taskqueue/) | In-memory task queue with high, normal, and low priority lanes, worker pools, retries, dead-letter handling, panic recovery, and backpressure. It is not a durable queue. |
| [`codec`](codec/) | Shared codec interface; JSON is the default implementation. |

### HTTP request binding

HTTP request fields can be populated from route parameters, the URL query, headers, cookies, or form data using struct tags. Dynamic `map[string]any` requests also receive URL query and path wildcard values, including the `any` request shape used by materialized Flow pipelines; a decoded JSON body takes precedence for duplicate keys. Request bodies use the configured codec (JSON by default). The HTTP adapter also provides reusable security-header middleware, request/execution/trace headers, SSE, and direct NDJSON iterator streaming. The Flow `thttp.listen` action auto-discovers every supported HTTP binding, including `HTTPRoute`, `SSERoute`, and `RawHTTPHandler`.

When NFlow `security_headers` is enabled, it uses the shared `thttp/middleware.SecurityHeaders` policy and separately marks `/api/` responses `Cache-Control: no-store`. The shared security middleware intentionally does not set CSP; applications serving HTML should provide their own policy.

### Nexss Flow 🌀 integration

Bundles under `thttp/nexssflow`, `tcli/nexssflow`, `tbus/nexssflow`, `tworker/nexssflow`, `cron/nexssflow`, `bus/nexssflow`, `bus/mediator/nexssflow`, and `bus/taskqueue/nexssflow` expose transport listeners, messaging actions, and bindings to Nexss Flow programs. Each flow uses `@require` to load its bundle and, for listeners, a binding modifier on a pipeline:

```
@require github.com/nexssp/transport/thttp/nexssflow { addr: ":8080" }
@pipeline hello:route="GET /hello"
  runtime.const @{ value: "Hello, Maks" }
@end
thttp.listen
```

```
@require github.com/nexssp/transport/tcli/nexssflow
@pipeline hello:cli="hello:Greet someone"
  runtime.const @{ value: "Hello, Julia" }
@end
tcli.listen
```

```
@require github.com/nexssp/transport/tbus/nexssflow
@pipeline orders:topic="orders"
  runtime.noop
@end
tbus.listen
```

```
@require github.com/nexssp/transport/tworker/nexssflow
@pipeline heartbeat:worker=1m
  runtime.const @{ value: "tick" }
@end
tworker.listen
```

`tworker` runs each `:worker` binding immediately, then on its configured interval. A successful result may implement `ContinueWorkerCycle() bool` to request an immediate next cycle (useful for draining work); otherwise the worker waits. `RestartOnError` bindings use Kernel's shared `action.ExponentialBackoff` by default (1s base, 30s maximum). `tworker.Options.Backoff` accepts a custom 1-based `func(attempt int) time.Duration`, including any Kernel action backoff helper. `tworker.WithOptions` supplies a runtime poll-interval override and payload-free lifecycle snapshots (`starting`, `running`, `idle`, `backoff`, `stopped`) without changing action or domain semantics.

```
@require github.com/nexssp/transport/cron/nexssflow
@pipeline hourly:cron="0 * * * *"
  runtime.const @{ value: "tick" }
@end
cron.listen
```

The generic bus exposes `bus.publish` (input `{ subject, payload }`), `bus.send` (input `{ name, payload }`), and `bus.listen`; bind subscribers with `subject` and command handlers with `command`:

```
@require github.com/nexssp/transport/bus/nexssflow
@pipeline orders:subject="orders"
  runtime.noop
@end
bus.listen
```

The mediator exposes `mediator.send` (input `{ name, payload }`) and `mediator.publish` (input `{ name, payload }`). Register command handlers with `command` and event subscribers with `event`:

```
@require github.com/nexssp/transport/bus/mediator/nexssflow
@pipeline createUser:command="users.create"
  runtime.noop
@end
mediator.listen
```

The task queue exposes `taskqueue.enqueue` (input `{ type, payload, priority }`, where priority is `high`, `normal`, or `low`) and `taskqueue.listen`; bind worker pipelines with `task`:

```
@require github.com/nexssp/transport/bus/taskqueue/nexssflow
@pipeline sendEmail:task="email.send"
  runtime.noop
@end
taskqueue.listen
```

Run a flow file with `nflow run <file>.nflow`. The repository's fuller HTTP samples are in [`nflows/`](nflows/).

> **Flow parity:** Flow supports HTTP routes, CLI commands, in-process topic pub/sub, interval workers, cron listeners, generic bus pub/sub and commands, mediator commands/events, and task queue producers/workers. Some advanced HTTP/CLI controls remain Go-only.

## Development commands

Local workflows and CI use [Task](https://taskfile.dev). Install it once:

```
go install github.com/go-task/task/v3/cmd/task@latest
```

List available targets and run the common checks:

```
task --list
task test                 # short test suite
task test -- ./thttp      # test one package
task test:race            # race-enabled tests
task bench                # benchmarks with memory statistics
task ci                   # repository CI gate
```

## Related projects

- [Nexss Kernel](https://github.com/nexssp/kernel) — typed actions and execution runtime.

- [Nexss Flow](https://github.com/nexssp/flow) — flow language and runtime for composing actions.

- [Task](https://taskfile.dev) — task runner used by this repository's local workflows and CI.
