import { afterAll, beforeAll, expect, test } from "bun:test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import express from "express";
import Fastify from "fastify";
import { Sipline } from "./index.js";

// Integration tests against the real Go server, built fresh from the repo root.
const bin = join(mkdtempSync(join(tmpdir(), "sipline-")), process.platform === "win32" ? "sipline.exe" : "sipline");
let server: ReturnType<typeof Bun.spawn>;
let port = 0;

async function start(addr: string) {
  server = Bun.spawn([bin], { env: { ...process.env, SIPLINE_ADDR: addr }, stderr: "pipe" });
  let out = "";
  const decoder = new TextDecoder();
  for await (const chunk of server.stderr as ReadableStream<Uint8Array>) {
    out += decoder.decode(chunk);
    const m = out.match(/listening on .*:(\d+)/);
    if (m) return Number(m[1]);
  }
  throw new Error(`server did not start: ${out}`);
}

const until = async (f: () => Promise<boolean>) => {
  for (let i = 0; i < 100; i++) {
    if (await f().catch(() => false)) return;
    await Bun.sleep(50);
  }
  throw new Error("condition not met");
};

const quiet = { onError: () => {} };

beforeAll(async () => {
  const build = Bun.spawnSync(["go", "build", "-o", bin, "."], { cwd: join(import.meta.dir, "..", "..") });
  if (!build.success) throw new Error(build.stderr.toString());
  port = await start("127.0.0.1:0");
}, 60_000); // a cold go build can exceed bun's 5s hook timeout
afterAll(() => server.kill());

test("take allows up to capacity, then denies", async () => {
  const l = new Sipline({ port, name: "take", capacity: 2, refillPerSec: 1, ...quiet });
  expect(await l.take("k")).toEqual({ allowed: true, remaining: 1, retryAfterMs: 0 });
  expect(await l.take("k")).toEqual({ allowed: true, remaining: 0, retryAfterMs: 0 });
  const r = await l.take("k");
  expect(r.allowed).toBe(false);
  expect(r.retryAfterMs).toBeGreaterThan(900);
  expect((await l.take("other")).allowed).toBe(true);
  await expect(l.take("has space")).rejects.toThrow("invalid key");
  l.close();
});

test("pipelined takes resolve in order", async () => {
  const l = new Sipline({ port, name: "pipe", capacity: 5, refillPerSec: 1, ...quiet });
  const rs = await Promise.all(Array.from({ length: 7 }, () => l.take("k")));
  expect(rs.map((r) => r.remaining)).toEqual([4, 3, 2, 1, 0, 0, 0]);
  expect(rs.map((r) => r.allowed)).toEqual([true, true, true, true, true, false, false]);
  l.close();
});

test("reconnects and re-sends CONFIG after a server restart", async () => {
  const errors: Error[] = [];
  const l = new Sipline({ port, name: "restart", capacity: 1, refillPerSec: 1, onError: (e) => errors.push(e) });
  expect((await l.take("k")).allowed).toBe(true);
  expect((await l.take("k")).allowed).toBe(false);

  server.kill();
  await server.exited;
  await until(async () => (await l.take("k").then(() => false, () => true)));
  expect(errors.length).toBeGreaterThan(0);

  await start(`127.0.0.1:${port}`);
  // Fresh server has no state: the bucket is full again, so the take is allowed.
  await until(async () => (await l.take("k")).allowed);
  l.close();
});

test("express middleware with custom key", async () => {
  const l = new Sipline<express.Request>({
    port, name: "express", capacity: 1, refillPerSec: 1, ...quiet,
    key: (req) => req.get("x-user") ?? "anon",
  });
  const app = express();
  app.use(l.middleware);
  app.get("/", (_req, res) => { res.send("ok"); });
  const http = app.listen(0);
  await new Promise((r) => http.once("listening", r));
  const url = `http://127.0.0.1:${(http.address() as any).port}/`;

  expect((await fetch(url, { headers: { "x-user": "a" } })).status).toBe(200);
  const denied = await fetch(url, { headers: { "x-user": "a" } });
  expect(denied.status).toBe(429);
  expect(denied.headers.get("retry-after")).toBe("1");
  expect((await fetch(url, { headers: { "x-user": "b" } })).status).toBe(200);
  http.close();
  l.close();
});

test("fastify hook with default ip key", async () => {
  const l = new Sipline({ port, name: "fastify", capacity: 1, refillPerSec: 1, ...quiet });
  const app = Fastify();
  app.addHook("onRequest", l.middleware);
  app.get("/", async () => "ok");
  const url = await app.listen({ port: 0, host: "127.0.0.1" });

  expect((await fetch(url)).status).toBe(200);
  const denied = await fetch(url);
  expect(denied.status).toBe(429);
  expect(denied.headers.get("retry-after")).toBe("1");
  await app.close();
  l.close();
});

test("key function errors go to the framework error handler", async () => {
  const l = new Sipline({ port, name: "badkey", capacity: 1, refillPerSec: 1, ...quiet, key: () => "a b" });
  const app = express();
  app.use(l.middleware);
  app.get("/", (_req, res) => { res.send("ok"); });
  const http = app.listen(0);
  await new Promise((r) => http.once("listening", r));
  expect((await fetch(`http://127.0.0.1:${(http.address() as any).port}/`)).status).toBe(500);
  http.close();
  l.close();
});

test("failOpen decides what happens when the server is unreachable", async () => {
  const dead = { port: 1, name: "dead", capacity: 1, refillPerSec: 1, ...quiet };
  const open = new Sipline(dead);
  const closed = new Sipline({ ...dead, failOpen: false });
  const app = Fastify();
  app.get("/open", { onRequest: open.middleware }, async () => "ok");
  app.get("/closed", { onRequest: closed.middleware }, async () => "ok");
  const url = await app.listen({ port: 0, host: "127.0.0.1" });
  expect((await fetch(`${url}/open`)).status).toBe(200);
  expect((await fetch(`${url}/closed`)).status).toBe(503);
  await app.close();
  open.close();
  closed.close();
});
