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
| `skip` | never | `(req) => boolean \| Promise<boolean>`. Return `true` to let a request through without limiting it. |
| `failOpen` | `true` | If the server can't be reached, `true` lets requests through and `false` answers `503`. |
| `timeoutMs` | `1000` | How long to wait for a reply before the connection is dropped and rebuilt. |
| `onError` | `console.error` | Called with connection errors, once per distinct error until the next successful connect. |

For TypeScript, pass the request type to get typed `key` and `skip` functions: `new Sipline<express.Request>({ ... })`.

## Limiting only some routes

The safest way is to attach the middleware only where you want it. The framework's router has already matched the path, so there is no way for an oddly written URL to slip past the limiter.

```ts
// Express: everything under /api, or a single route
app.use("/api", limiter.middleware);
app.post("/login", limiter.middleware, loginHandler);

// Fastify: every route in a plugin, or a single route
fastify.register(async (api) => {
  api.addHook("onRequest", limiter.middleware);
  api.get("/items", itemsHandler);
}, { prefix: "/api" });
fastify.post("/login", { onRequest: limiter.middleware }, loginHandler);
```

Use `skip` when the decision doesn't depend on the route, or when you'd rather keep one global hook:

```ts
const limiter = new Sipline({
  name: "api", capacity: 10, refillPerSec: 2,
  skip: (req) => req.method === "OPTIONS" || req.user?.isAdmin,
});
```

With a global Fastify hook, a route can opt out through its config:

```ts
const limiter = new Sipline({
  name: "api", capacity: 10, refillPerSec: 2,
  skip: (req) => req.routeOptions?.config?.sipline === false,
});
fastify.addHook("onRequest", limiter.middleware);
fastify.get("/health", { config: { sipline: false } }, healthHandler);
```

## Reconnects

If the connection drops, commands still waiting for a reply fail right away, and `failOpen` decides what happens to those requests. The client then reconnects with a delay that starts at 100 ms and doubles up to 5 s. After every reconnect it configures the limiter again, so a server restart needs no action on your side. The server keeps state only in memory, so all buckets start full again after a restart.

## Development

See [DEVELOPMENT.md](https://github.com/realChriss/sipline/blob/main/clients/ts/DEVELOPMENT.md) for building, testing and publishing.
