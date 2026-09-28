import { connect, type Socket } from "node:net";

export interface SiplineOptions<Req = any> {
  /** Server host. Default 127.0.0.1. */
  host?: string;
  /** Server port. Default 7700. */
  port?: number;
  /** Limiter name, shared by every client that uses the same name. */
  name: string;
  /** Bucket size: how many requests a key may burst. Integer 1..1000000. */
  capacity: number;
  /** Tokens added per second. Integer 1..1000000. */
  refillPerSec: number;
  /** Builds the bucket key from a request. Default: the client IP. */
  key?: (req: Req) => string | Promise<string>;
  /** Return true to let a request through without limiting it. Runs before `key`. Default: never skip. */
  skip?: (req: Req) => boolean | Promise<boolean>;
  /** Let requests through when the server can't be reached. Default true. */
  failOpen?: boolean;
  /** Per-command timeout; on expiry the connection is dropped and rebuilt. Default 1000. */
  timeoutMs?: number;
  /** Connection problems. Default: console.error, once per distinct error until the next successful connect. */
  onError?: (err: Error) => void;
}

export interface TakeResult {
  allowed: boolean;
  remaining: number;
  retryAfterMs: number;
}

type Pending = { resolve: (line: string) => void; reject: (err: Error) => void; timer: ReturnType<typeof setTimeout> };

// Mirrors validName in main.go: 1..256 bytes, no whitespace or control characters.
const validName = (s: unknown): s is string =>
  typeof s === "string" && /^[^\x00-\x20\x7f]+$/.test(s) && Buffer.byteLength(s) <= 256;
const validCount = (n: number) => Number.isInteger(n) && n >= 1 && n <= 1_000_000;

export class Sipline<Req = any> {
  private readonly host: string;
  private readonly port: number;
  private readonly name: string;
  private readonly configLine: string;
  private readonly key: (req: Req) => string | Promise<string>;
  private readonly skip: (req: Req) => boolean | Promise<boolean>;
  private readonly failOpen: boolean;
  private readonly timeoutMs: number;
  private readonly onError: (err: Error) => void;

  private socket?: Socket;
  private buf = "";
  private pending: Pending[] = [];
  private backoff = 100;
  private retryTimer?: ReturnType<typeof setTimeout>;
  private lastError = "";
  private closed = false;

  constructor(opts: SiplineOptions<Req>) {
    if (!validName(opts.name)) throw new TypeError(`sipline: invalid limiter name ${JSON.stringify(opts.name)}`);
    if (!validCount(opts.capacity) || !validCount(opts.refillPerSec))
      throw new TypeError("sipline: capacity and refillPerSec must be integers from 1 to 1000000");
    this.host = opts.host ?? "127.0.0.1";
    this.port = opts.port ?? 7700;
    this.name = opts.name;
    this.configLine = `CONFIG ${opts.name} ${opts.capacity} ${opts.refillPerSec}`;
    this.key = opts.key ?? ((req: any) => req.ip ?? req.socket?.remoteAddress);
    this.skip = opts.skip ?? (() => false);
    this.failOpen = opts.failOpen ?? true;
    this.timeoutMs = opts.timeoutMs ?? 1000;
    this.onError = opts.onError ?? ((err) => console.error(`sipline: ${err.message}`));
    this.connect();
  }

  /** Takes one token for `key`. Rejects if the server is unreachable or the key is invalid. */
  async take(key: string): Promise<TakeResult> {
    if (!validName(key)) throw new TypeError(`sipline: invalid key ${JSON.stringify(key)}`);
    const cmd = `TAKE ${this.name} ${key}`;
    let reply = await this.send(cmd);
    if (reply === "ERR unknown limiter") reply = (await Promise.all([this.send(this.configLine), this.send(cmd)]))[1];
    const [word, n] = reply.split(" ");
    if (word === "ALLOW") return { allowed: true, remaining: Number(n), retryAfterMs: 0 };
    if (word === "DENY") return { allowed: false, remaining: 0, retryAfterMs: Number(n) };
    throw new Error(`sipline: ${reply}`);
  }

  /**
   * Express: app.use(limiter.middleware)
   * Fastify: app.addHook("onRequest", limiter.middleware)
   * Answers 429 with Retry-After when denied, 503 when the server is down and failOpen is false.
   */
  middleware = (req: Req, res: any, next: (err?: Error) => void): void => {
    this.check(req).then((r) => {
      if (r === "skip" || (r ? r.allowed : this.failOpen)) return next();
      const status = r ? 429 : 503;
      const headers: Record<string, string> = r ? { "Retry-After": String(Math.ceil(r.retryAfterMs / 1000)) } : {};
      const body = r ? "Too Many Requests" : "Service Unavailable";
      if (typeof res.code === "function") res.code(status).headers(headers).send(body); // fastify reply
      else res.writeHead(status, headers).end(body); // express / node:http response
    }, next);
  };

  /** "skip" when the request is exempt, null when the server couldn't answer. */
  private async check(req: Req): Promise<TakeResult | null | "skip"> {
    if (await this.skip(req)) return "skip";
    const key = await this.key(req);
    if (!validName(key)) throw new TypeError(`sipline: key function returned invalid key ${JSON.stringify(key)}`);
    return this.take(key).catch(() => null);
  }

  /** Closes the connection and stops reconnecting. */
  close(): void {
    this.closed = true;
    clearTimeout(this.retryTimer);
    this.socket?.destroy();
  }

  private connect() {
    const s = connect(this.port, this.host);
    this.socket = s;
    s.setEncoding("utf8");
    s.setNoDelay(true);
    s.unref();
    // Written before the connect completes, so it is always the first command on the wire.
    this.send(this.configLine).then((reply) => {
      if (reply !== "OK") return s.destroy(new Error(`CONFIG failed: ${reply}`));
      this.backoff = 100;
      this.lastError = "";
    }, () => {});
    s.on("data", (chunk: string) => {
      this.buf += chunk;
      for (let i; (i = this.buf.indexOf("\n")) >= 0; ) {
        const line = this.buf.slice(0, i).replace(/\r$/, "");
        this.buf = this.buf.slice(i + 1);
        const p = this.pending.shift();
        if (!p) return void s.destroy(new Error(`unexpected reply: ${line}`));
        clearTimeout(p.timer);
        p.resolve(line);
      }
    });
    s.on("error", (err) => {
      if (err.message === this.lastError) return;
      this.lastError = err.message;
      this.onError(err);
    });
    s.on("close", () => {
      this.socket = undefined;
      this.buf = "";
      for (const p of this.pending.splice(0)) {
        clearTimeout(p.timer);
        p.reject(new Error("sipline: connection closed"));
      }
      if (this.closed) return;
      this.retryTimer = setTimeout(() => this.connect(), this.backoff);
      this.retryTimer.unref();
      this.backoff = Math.min(this.backoff * 2, 5000);
    });
  }

  private send(line: string): Promise<string> {
    const s = this.socket;
    if (!s || s.destroyed) return Promise.reject(new Error("sipline: not connected"));
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => s.destroy(new Error("timeout")), this.timeoutMs);
      this.pending.push({ resolve, reject, timer });
      s.write(line + "\n");
    });
  }
}
