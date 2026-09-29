# Integrations (stage 27)

The agent reads and acts on the user's real accounts. Every service
follows one shared pattern - OAuth connect, read tools, write tools -
so adding the next one is a known shape, not a new design.

## The pattern

- `internal/oauth`: authorization-code flow, token exchange, refresh,
  and the token store (`data/tokens.json`, 0600, atomic writes). The
  store is NOT encrypted at rest; that is roadmap stage 8 and the code
  says so instead of faking crypto.
- Connect handler: `/oauth/<service>/start` redirects to the
  provider's consent screen; `/oauth/<service>/callback` verifies the
  single-use state, exchanges the code and stores the token. It mounts
  on the same listener as the WhatsApp webhook (like links, stage 24).
- Per-service package (e.g. `internal/google`): the API client with
  base URLs as variables, so CI runs everything against fake servers.
  No test ever touches a real account.
- Tools (e.g. `gmail_search`, `gmail_send`): registered only when the
  integration is enabled; with no connected token they answer an
  honest "not connected" error, never a fake empty result.

## Gmail (27a)

Read: `gmail_search` (query, ids, from/subject/date/snippet).
Write: `gmail_send` (plain-text email; the tool description tells the
model to confirm recipient, subject and body first).

Setup:

1. In Google Cloud Console create an OAuth client (type: web
   application) with redirect URI
   `https://<your-bot-host>/oauth/gmail/callback`.
2. In `fiveagent.yml`:

```yaml
integrations:
  gmail:
    enabled: true
    client_id: "....apps.googleusercontent.com"
    client_secret: "..."
    redirect_url: "https://<your-bot-host>"
    # token_path: "data/tokens.json"   # default
```

3. Restart, open `https://<your-bot-host>/oauth/gmail/start`, consent,
   done. The token refreshes itself; the refresh is saved back to the
   store.

CI covers the whole flow against fake endpoints (form fields, state
checks, 0600 store, transparent refresh, RFC822 payload, auth header
on every call). Live verification with a real Google app is pending -
it needs the operator's client credentials, same as the media services.

## Google Calendar (27b)

The pattern copy: same token store, same connect handler
(`/oauth/calendar/start`), same honest-error tools.

Read: `calendar_list` (events in a range; RFC3339 or bare dates).
Write: `calendar_create` (summary, start/end RFC3339, optional
location, description, attendees). `FreeBusy` is in the client for the
next slice.

```yaml
integrations:
  calendar:
    enabled: true
    client_id: "....apps.googleusercontent.com"
    client_secret: "..."
    redirect_url: "https://<your-bot-host>"
```

Both Google services share `data/tokens.json` under different keys
("gmail", "calendar"), so each asks for its own consent with its own
scope today; merging Google scopes into one consent is a documented
future refinement, not hidden magic. CI covers list/create/free-busy
parsing, the create payload, auth headers and the honest
not-connected path against fake endpoints.
