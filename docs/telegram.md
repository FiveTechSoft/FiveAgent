# Telegram setup

FiveAgent talks to Telegram through the official Bot API with **long
polling**: the agent calls out to Telegram, so it works on any PC behind
NAT. No public URL, no tunnel, no open ports.

## Setup (5 minutes)

1. On Telegram, open **@BotFather** and send `/newbot`. Follow the prompts
   (name, then a username ending in `bot`). BotFather replies with a token
   like `123456:ABC-DEF...`.
2. In `fiveagent.yml`:

   ```yaml
   channels:
     telegram:
       enabled: true
       bot_token: "123456:ABC-DEF..."
   ```

3. Start FiveAgent. The log shows `telegram: connected as @your_bot`.
4. Open your bot in Telegram and send it a message.

That's it. Keep the token secret: anyone with it controls the bot.

## What works today

- Long polling with offset tracking and retry backoff. Token checked at
  startup (`getMe`), so a wrong token fails fast with a clear error.
- Inbound: text, photos, voice messages, documents, video, stickers and
  locations, described to the agent (e.g. `[photo: caption]`).
- Outbound: text with quoted reply to your message (`SendText`), media by
  URL (`SendMedia`), "typing..." indicator while the agent thinks.
- Plain text only: no Markdown parsing, so model output can never break
  Telegram formatting.
- Unit tests cover the full flow with a stub Bot API server:
  `go test ./internal/channel/`.

## Missing today

- Downloading inbound media bytes (photos, voice) for the agent to
  inspect - needs a tool wired to `getFile`.
- Voice message transcription (needs a speech-to-text tool).
- Group chats: the bot answers in private chats; group behavior is
  untested.
