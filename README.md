# sipline

A small in-memory token-bucket rate limiter. Apps on the same machine connect over TCP, send a key (usually a client IP) and get back whether the request is allowed. It's one Go binary that uses only the standard library.

## Build

```sh
go build .                                              # current platform
build.sh / build.bat                                    # all platforms -> dist/
go test ./...
```

`sh release.sh` picks the next version, tags `main` and pushes the tag. GitHub Actions then builds all platforms and publishes a release.

## Run

```sh
sipline                        # listens on 127.0.0.1:7700
sipline --addr 0.0.0.0:7700    # accept remote clients (no auth! firewall it)
sipline --sweep 30s            # evict idle buckets every 30s (default 60s)
sipline --log sipline.log      # also append logs, including every request, to a file
```

Without `--log`, the server prints only its startup line and errors to stderr. With `--log`, the file gets those lines plus one line per connection, disconnection and request:

```
2026/09/26 17:35:55 127.0.0.1:50344 "TAKE api 1.2.3.4" ALLOW 2
```

The file is written in batches and flushed every second and on Ctrl+C, so a crash can lose the last second of lines. There is no rotation, so the file grows until you delete or rotate it yourself. Request logging slows the server down under heavy load.

## Protocol

Send one command per line and get one reply per line back, in order. You can pipeline commands.

| Command | Reply |
| --- | --- |
| `CONFIG <limiter> <capacity> <refill_per_sec>` | `OK` |
| `TAKE <limiter> <key>` | `ALLOW <remaining>` / `DENY <retry_ms>` |
| `PING` | `PONG` |

`capacity` and `refill_per_sec` are whole numbers from 1 to 1000000. Anything else, such as `0.5`, `1e3` or `+1`, is rejected. The server holds at most 1000 limiters and never deletes them.

Errors are `ERR unknown command`, `ERR bad arguments`, `ERR unknown limiter`, `ERR too many limiters` and `ERR line too long` (the last one also closes the connection).

The server accepts at most 1000 connections at once. Beyond that, a new connection gets `ERR too many connections` and is closed. Idle connections stay open; TCP keepalive closes connections to clients that have died.

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
