# sipline protocol

This is the contract every sipline client follows. `main.go` is the reference implementation.

## Transport

A client opens a plain TCP connection (default `127.0.0.1:7700`) and keeps it open. There is no handshake, auth or TLS.

Each message is one line of text ending in `\n` (`\r\n` is accepted too). The newline marks the end of a message, and the connection stays open for the next one. Fields are separated by exactly one space, and commands are case-sensitive.

Every command gets exactly one reply line, in the order the commands were sent. Clients may pipeline: send several commands without waiting and match replies to commands in order. Lines longer than 1024 bytes are rejected.

## Commands

| Command | Reply |
| --- | --- |
| `CONFIG <limiter> <capacity> <refill_per_sec>` | `OK` |
| `TAKE <limiter> <key>` | `ALLOW <remaining>` or `DENY <retry_ms>` |
| `PING` | `PONG` |

- `<limiter>` and `<key>` are 1 to 256 bytes with no spaces, tabs or other control characters. UTF-8 is fine.
- `<capacity>` and `<refill_per_sec>` are integers from 1 to 1000000, written in plain digits.

### CONFIG

This command creates a limiter, or changes the capacity and refill rate of an existing one. Changing a limiter keeps its buckets. The server holds at most 1000 limiters.

### TAKE

This command takes one token from the bucket for `<key>` in `<limiter>`. Every key has its own bucket. A new bucket starts full with `<capacity>` tokens and refills continuously at `<refill_per_sec>` tokens per second, up to `<capacity>`.

- `ALLOW <remaining>`: the request is allowed. `<remaining>` is the number of whole tokens left after this one.
- `DENY <retry_ms>`: the request is denied, and no token was taken. `<retry_ms>` is the number of milliseconds until the next token is available (at least 1).

Buckets that have been idle long enough to refill completely are deleted. The client can't tell, because a new bucket starts full anyway.

### PING

The reply is always `PONG`. Use it for health checks.

## Errors

| Reply | Cause | Connection |
| --- | --- | --- |
| `ERR unknown command` | The first field is not a known command (this includes empty lines and lowercase commands). | stays open |
| `ERR bad arguments` | Wrong number of fields, an invalid name or an invalid number. | stays open |
| `ERR unknown limiter` | A `TAKE` names a limiter that was never configured, or the server restarted. | stays open |
| `ERR too many limiters` | A `CONFIG` would create a limiter past the limit of 1000. | stays open |
| `ERR line too long` | The line is over 1024 bytes. | closed by the server |
| `ERR too many connections` | Sent right after connecting when the server already has 1000 connections. | closed by the server |

## Example

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

## Client rules

All state lives in memory and is lost when the server restarts. A client recovers on its own if it does the following:

- Send `CONFIG` for every limiter right after connecting, including after every reconnect.
- If `TAKE` returns `ERR unknown limiter`, send `CONFIG` again and retry the `TAKE` once.
- If the connection drops, fail all commands still waiting for a reply and reconnect with backoff. Keep a timeout on waiting commands too. Because replies are matched by order, a missing reply means the connection can't be trusted any more, so close it and reconnect.
- Keep one long-lived connection per client, not one per request.
- Check limiter names, keys and numbers before sending them, so that a bad key from the application doesn't show up as `ERR bad arguments`.
- If the server can't be reached, decide yourself whether to fail open (allow) or fail closed (deny), and let the user configure it.
