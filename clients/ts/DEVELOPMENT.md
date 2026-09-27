# Developing the TypeScript client

You need Bun and Go. The tests build `main.go` from the repo root and run against the real server.

```sh
bun install
bun run test       # type check + tests
bun run build      # -> dist/
```

## Publishing to npm

The package is published as [`sipline`](https://www.npmjs.com/package/sipline).

### One-time setup

1. Turn on two-factor authentication for your npm account (avatar → **Account** → **Two-Factor Authentication**). npm refuses to publish without it.
2. Create a token: avatar → **Access Tokens** → **Generate New Token** → **Granular Access Token**.
   - **Packages and scopes:** *Read and write (publish and stage)*. "Stage only" would need a manual approval on npmjs.com.
   - **Organizations:** *No access*.
   - Leave "Bypass two-factor authentication" off.
3. Store the token in your user folder, never in the repo:
   ```sh
   echo "//registry.npmjs.org/:_authToken=npm_YOUR_TOKEN" >> ~/.npmrc
   bun pm whoami   # prints your npm username
   ```

Write tokens expire. When publishing fails with an auth error, create a new token and replace the line in `~/.npmrc`.

### Each release

1. Raise `"version"` in `package.json`, for example `0.1.1` for fixes or `0.2.0` for new features. npm never accepts the same version twice, not even after an unpublish.
2. Commit and push.
3. Publish, with the current code from your authenticator app. The tests and build run first, so type the command before you copy the code:
   ```sh
   bun publish --otp=123456
   ```
   `bun publish --dry-run` shows what would be uploaded without logging in.
4. Tag the published commit, from anywhere in the repo:
   ```sh
   git tag ts-v0.1.1 <commit> && git push origin ts-v0.1.1
   ```
   The `ts-` prefix keeps the server release workflow (`v*` tags) from running. Don't use `release.sh` for the client: it creates server tags.
