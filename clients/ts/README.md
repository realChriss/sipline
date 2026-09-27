# sipline

Client for the [sipline](https://github.com/realChriss/sipline) rate limiter server, for Node and Bun.

```sh
bun add sipline    # or: npm install sipline
```

You also need a running sipline server. See the [main README](https://github.com/realChriss/sipline#readme).

## Usage

```ts
import { Sipline } from "sipline";

const limiter = new Sipline({
  host: "127.0.0.1",
  port: 7700,
  name: "api",       // limiter name on the server
  capacity: 10,      // burst size per key
  refillPerSec: 2,   // tokens added per second
  key: (req) => (req.user ? `user:${req.user.id}` : req.ip),
});

app.use(limiter.middleware);                        // Express
fastify.addHook("onRequest", limiter.middleware);   // Fastify

await limiter.take("some-key"); // { allowed, remaining, retryAfterMs }
limiter.close();
```

Denied requests get `429 Too Many Requests` with a `Retry-After` header in seconds.

Create one `Sipline` per limiter and reuse it. Each one keeps a single connection open to the server.

## Options

| Option | Default | Meaning |
| --- | --- | --- |
| `host` | `127.0.0.1` | Server host. |
| `port` | `7700` | Server port. |
| `name` | required | Limiter name. Clients that use the same name share buckets, so they must also use the same `capacity` and `refillPerSec`; otherwise each reconnect overwrites the other's settings. |
| `capacity` | required | How many requests a key may make in a burst. Integer from 1 to 1000000. |
| `refillPerSec` | required | Tokens added per second. Integer from 1 to 1000000. |
| `key` | `req.ip` | `(req) => string \| Promise<string>`. Returns the bucket key for a request. The key must be 1 to 256 bytes with no whitespace; otherwise the request goes to the framework's error handler. |
| `failOpen` | `true` | If the server can't be reached, `true` lets requests through and `false` answers `503`. |
| `timeoutMs` | `1000` | How long to wait for a reply before the connection is dropped and rebuilt. |
| `onError` | `console.error` | Called with connection errors, once per distinct error until the next successful connect. |

For TypeScript, pass the request type to get a typed `key` function: `new Sipline<express.Request>({ ... })`.

## Reconnects

If the connection drops, commands still waiting for a reply fail right away, and `failOpen` decides what happens to those requests. The client then reconnects with a delay that starts at 100 ms and doubles up to 5 s. After every reconnect it configures the limiter again, so a server restart needs no action on your side. The server keeps state only in memory, so all buckets start full again after a restart.

## Development

The tests build and run the real Go server, so Go must be installed.

```sh
bun install
bun run test       # type check + tests
bun run build      # -> dist/
bun publish        # runs the tests and the build first
```
