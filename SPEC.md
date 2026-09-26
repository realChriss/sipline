# Localhost Rate Limiter Spec

## Overview

A small standalone server that rate-limits requests per key (usually a client IP) with a token bucket. Any app on the same machine connects over TCP, sends a key, and gets back whether the request is allowed and how many requests remain. All state lives in memory; there is no database.

**Goals**

- One self-contained binary for Windows, Linux and macOS, written in Go with only the standard library
- A tiny text protocol any language can speak over a plain TCP connection
- Clients set the limits at runtime, so no config files are needed
- Small codebase: roughly 200–300 lines

**Non-goals for v1**

- Persistence: a restart wipes all buckets
- Clustering or sharing state across machines
- Authentication or TLS; exposing the port beyond localhost is the operator's choice
- HTTP/JSON API, metrics or dashboards
- Weighted requests: every request costs exactly 1 token

## Running the server

The server speaks TCP only and listens on `127.0.0.1:7700` by default. The operator decides how far to expose it: keep the default for local-only use, or bind to another address to accept remote clients.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--addr` | `127.0.0.1:7700` | TCP address to listen on, e.g. `0.0.0.0:7700` to accept connections from other machines. |
| `--sweep` | `60s` | How often idle buckets are evicted. |

- There is no authentication, so anything that can reach the port can send `CONFIG` and `TAKE`. When binding beyond localhost, restrict access with a firewall or private network.
- Logs go to stderr: one line at startup, plus errors. No per-request logging.

## Protocol

Clients send one command per line and get exactly one response line back, in the same order. Connections are long-lived, and a client may send several commands without waiting for replies (pipelining).

**Format rules**

- UTF-8 text, fields separated by a single space, each line ends with `\n` (a trailing `\r` is ignored)
- Commands are case-sensitive and uppercase
- Limiter names and keys: 1–256 bytes, no spaces or control characters
- Maximum line length is 1 KB; a longer line gets `ERR line too long` and the connection is closed

**Commands**

| Command | Response | Purpose |
| --- | --- | --- |
| `CONFIG <limiter> <capacity> <refill_per_sec>` | `OK` | Create or update a named limiter. `capacity` is a positive integer; `refill_per_sec` is a positive number and may be fractional (e.g. `0.5`). |
| `TAKE <limiter> <key>` | `ALLOW <remaining>` or `DENY <retry_ms>` | Try to spend one token from the key's bucket. |
| `PING` | `PONG` | Health check. |

**Response details**

- `ALLOW <remaining>`: the request is allowed; `remaining` is the number of whole tokens left after this one (an integer ≥ 0).
- `DENY <retry_ms>`: the request is limited; `retry_ms` is the milliseconds until one token is available, rounded up. Clients can use it for a `Retry-After` header.
- `ERR <message>`: the command failed and nothing changed. Messages: `unknown command`, `bad arguments`, `unknown limiter`, `line too long`.

**CONFIG on an existing limiter**

Calling `CONFIG` again with the same values does nothing, so every client can safely send it at startup. New values apply immediately; existing buckets keep their tokens, clamped down to the new capacity.

## Token bucket

Each key gets a bucket that refills lazily when it's used; there are no timers. A bucket stores two fields: `tokens` (a float) and `last` (the time of its last update, from a monotonic clock).

On `TAKE`:

1. If the key has no bucket, create one that is full: `tokens = capacity`.
2. Refill: `tokens = min(capacity, tokens + (now - last) × refill_per_sec)`, then set `last = now`.
3. If `tokens ≥ 1`: subtract 1 and reply `ALLOW floor(tokens)`.
4. Otherwise reply `DENY ceil((1 - tokens) / refill_per_sec × 1000)`.

A denied request spends no tokens. Example: with capacity 10 and 1 token per second, a client can burst 10 requests, then gets about one per second after that.

## Concurrency and memory

Each connection runs in its own goroutine. Each limiter holds a `map[key]*bucket` guarded by one mutex; a second mutex guards the map of limiters. That is enough for tens of thousands of checks per second. Sharding the map is left out of v1 and only worth adding if profiling shows lock contention.

**Eviction**

Every `--sweep` interval, a background goroutine deletes buckets that have been idle long enough to be full again: `now - last ≥ capacity / refill_per_sec`. This is safe, because a returning key gets a new full bucket, which is exactly what the old one would have held. Memory therefore tracks active keys rather than every key ever seen.

## Restarts and client expectations

A restart loses all limiters and buckets. Clients recover automatically if they follow these rules:

- Send `CONFIG` for each limiter right after connecting, including after any reconnect.
- If `TAKE` returns `ERR unknown limiter`, send `CONFIG` again and retry once.
- If the server is unreachable, decide in the client whether to allow (fail open) or reject (fail closed). The server has no say in this.

## Example session

A limiter named `api` allowing bursts of 3 and refilling 1 token per second (`>` is sent, `<` is received):

```
> CONFIG api 3 1
< OK
> TAKE api 203.0.113.7
< ALLOW 2
> TAKE api 203.0.113.7
< ALLOW 1
> TAKE api 203.0.113.7
< ALLOW 0
> TAKE api 203.0.113.7
< DENY 1000
> TAKE login 203.0.113.7
< ERR unknown limiter
```

## Open questions

- [x] Named limiters (e.g. a strict `login` and a looser `api`) or a single global limit? Named costs only a few extra lines. **Decided: named.**
- [x] Is 7700 a good default port? **Decided: keep 7700.**
- [x] Should `TAKE` accept an optional cost later, or stay fixed at 1 token? **Decided: fixed at 1 for v1; an optional 4th argument can be added later without breaking clients.**