# sipline

A small in-memory token-bucket rate limiter. Apps on the same machine connect over TCP, send a key (usually a client IP) and get back whether the request is allowed. It's one Go binary that uses only the standard library.

## Run with Docker Compose

```sh
mkdir sipline && cd sipline
curl -fsSL -o docker-compose.yaml https://raw.githubusercontent.com/realChriss/sipline/main/docker-compose.prod.yaml
docker compose up -d
```

This starts `ghcr.io/realchriss/sipline:latest` on `127.0.0.1:7700`. Keep the `127.0.0.1:` prefix in `ports` unless remote clients should reach it, because there is no auth.

Without Compose:

```sh
docker run -d -p 127.0.0.1:7700:7700 ghcr.io/realchriss/sipline
```

## Configuration

Configure the server with environment variables:

| Variable | Default | Meaning |
| --- | --- | --- |
| `SIPLINE_ADDR` | `127.0.0.1:7700` (`0.0.0.0:7700` in Docker) | Address to listen on. `0.0.0.0:7700` accepts remote clients (no auth! firewall it). |
| `SIPLINE_SWEEP` | `60s` | How often idle buckets are evicted. |
| `SIPLINE_LOG` | empty | File to append logs to, including every request. |

In Docker, point `SIPLINE_LOG` into a mounted directory, as the commented lines in `docker-compose.prod.yaml` show.

Without `SIPLINE_LOG`, the server prints only its startup line and errors to stderr. With it, the file gets those lines plus one line per connection, disconnection and request:

```
2026/09/26 17:35:55 127.0.0.1:50344 "TAKE api 1.2.3.4" ALLOW 2
```

The file is written in batches and flushed every second and on shutdown, so a crash can lose the last second of lines. There is no rotation, so the file grows until you delete or rotate it yourself. Request logging slows the server down under heavy load.

## Build

```sh
go build .                                              # current platform
build.sh / build.bat                                    # all platforms -> dist/
go test ./...
SIPLINE_SWEEP=30s ./sipline                             # run the binary
```

## Protocol

Send one command per line and get one reply per line back, in order. You can pipeline commands.

| Command | Reply |
| --- | --- |
| `CONFIG <limiter> <capacity> <refill_per_sec>` | `OK` |
| `TAKE <limiter> <key>` | `ALLOW <remaining>` / `DENY <retry_ms>` |
| `PING` | `PONG` |

Errors are `ERR unknown command`, `ERR bad arguments`, `ERR unknown limiter`, `ERR too many limiters` and `ERR line too long` (the last one also closes the connection).

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
