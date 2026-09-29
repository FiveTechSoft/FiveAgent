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
