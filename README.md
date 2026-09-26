# sipline

A small in-memory token-bucket rate limiter. Apps on the same machine connect over TCP, send a key (usually a client IP) and get back whether the request is allowed. It's one Go binary that uses only the standard library. The full protocol and design are in [SPEC.md](SPEC.md).

## Build

```sh
go build .                                              # current platform
powershell -ExecutionPolicy Bypass -File build.ps1      # all platforms -> dist/
go test ./...
```

## Run

```sh
sipline                        # listens on 127.0.0.1:7700
sipline --addr 0.0.0.0:7700    # accept remote clients (no auth! firewall it)
sipline --sweep 30s            # evict idle buckets every 30s (default 60s)
```

## Protocol

Send one command per line and get one reply per line back, in order. You can pipeline commands.

| Command | Reply |
| --- | --- |
| `CONFIG <limiter> <capacity> <refill_per_sec>` | `OK` |
| `TAKE <limiter> <key>` | `ALLOW <remaining>` / `DENY <retry_ms>` |
| `PING` | `PONG` |

Errors are `ERR unknown command`, `ERR bad arguments`, `ERR unknown limiter` and `ERR line too long` (the last one also closes the connection).

```
> CONFIG api 3 1
< OK
> TAKE api 203.0.113.7
< ALLOW 2
```

## Client rules

All state is lost when the server restarts. Clients recover on their own if they do the following:

- Send `CONFIG` for every limiter right after connecting, including after a reconnect.
- If `TAKE` returns `ERR unknown limiter`, send `CONFIG` again and retry once.
- If the server can't be reached, decide yourself whether to fail open or fail closed.
