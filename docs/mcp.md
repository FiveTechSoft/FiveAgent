# FiveAgent as an MCP server

Stage 33 of the roadmap. FiveAgent can expose a local,
token-authenticated MCP endpoint so an external agent - the design
consumer is the owner's OpenCode - runs commands inside the owner's
sandbox and reads files from the workspace folder, without ever
touching anything outside them.

## From zero to the first call

1. Enable the sandbox and the MCP server in `fiveagent.yml`:

   ```yaml
   sandbox:
     enabled: true
   mcp:
     enabled: true
     token: "pick-a-long-random-token"
     # listen_addr: "127.0.0.1:8090"   # default; keep it on loopback
     # user_key: "mcp"                 # sandbox folder for MCP calls
   ```

2. Restart FiveAgent. The log confirms:
   `MCP server: http://127.0.0.1:8090/mcp (bearer token required)`.

3. First call (any MCP client, or curl for a smoke test):

   ```bash
   curl -X POST http://127.0.0.1:8090/mcp \
     -H 'Authorization: Bearer pick-a-long-random-token' \
     -H 'Content-Type: application/json' \
     -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
   ```

   The answer carries `protocolVersion`, `capabilities` and
   `serverInfo` (`fiveagent`). Then `tools/list` shows the two tools
   and `tools/call` runs them.

## MCP client config

For a client that speaks streamable HTTP (OpenCode and most current
clients), the server entry is:

```json
{
  "fiveagent": {
    "type": "remote",
    "url": "http://127.0.0.1:8090/mcp",
    "headers": { "Authorization": "Bearer pick-a-long-random-token" }
  }
}
```

The exact key names for the client entry vary between MCP client
versions - check your client's docs if it rejects the block above.
The server side (URL, POST, bearer token) is what FiveAgent
implements and is covered by the CI battery.

## The tools

- `fiveagent_run_command` - `{argv: ["go", "test", "./..."]}`. Runs
  inside the sandbox as raw argv (no shell). Returns stdout, stderr
  and the exit code; a non-zero exit or a timeout comes back as an
  honest `isError` result, never as a fake success.
- `fiveagent_read_file` - `{path: "notes/todo.md"}`. Reads a file from
  the workspace folder; `..` escapes are refused.

## Errors you can trust

- Missing or wrong token: HTTP 401 with a JSON-RPC error body.
- Unknown method: `-32601`. Unknown tool: `-32602`.
- Sandbox refusal, non-zero exit, read failure: `isError: true` with
  the real stderr / error text.

## What the battery proves

`evals/mcp_test.go` drives the endpoint with a fake client over real
HTTP: auth refusals, the full initialize/list/call flow, commands
reaching the sandbox as argv under the `mcp` user key (the package's
only execution path - it never execs directly), workspace reads
confined to the folder, and every failure mode surfacing honestly.
The live check with the owner's OpenCode is on the user's
verification queue.

## Hardening and known limits

Enforced by the server (each has a test in `evals/mcp_security_test.go`):

- The token must be sent as `Authorization: Bearer <token>`; a bare
  token or another scheme gets 401. Tokens shorter than 16 characters
  are refused at startup.
- Any request carrying an `Origin` header is refused with 403, even with
  the right token: command-line clients do not send one, browsers do.
- On a loopback listener the `Host` header must be `localhost`,
  `127.0.0.1` or `::1`, which stops DNS-rebinding pages. On other binds
  this check is off.
- At most 4 commands run at once; extra calls get an honest busy error.
- The server does not start on the `jobobject` backend, which has no
  filesystem or network isolation. Use bubblewrap, docker or
  AppContainer.

Known limits, not fixed:

- Commands run in the sandbox folder `data/sandbox/<user_key>`, while
  `fiveagent_read_file` reads `data/workspace/mcp-server`. They are
  different folders, so a file a command writes is not visible to
  `fiveagent_read_file`.
- MCP calls do not go through the chat confirmation for effectful
  tools: a client holding the token is trusted to run sandboxed
  commands.
- There is no rate limit on wrong tokens beyond the loopback bind and
  the token length.
