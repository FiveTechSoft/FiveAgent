# WhatsApp setup, step by step (with pictures)

Goal: when someone writes to your WhatsApp number, the message reaches
FiveAgent running on your own PC, and the agent answers.

You need three things, all free:

- **A Meta developer app** - gives you a test phone number and a token.
- **cloudflared** - gives your PC a public web address (one command).
- **fiveagent.yml** - the file where you paste the values.

Total time: about 15 minutes the first time.

---

## Step 1 - Create the Meta app and copy 2 values

1. Go to https://developers.facebook.com and log in with a Facebook account.
2. **Mis aplicaciones** → **Crear app** → type **Otro** → **Empresa**,
   name it whatever you like.
3. In the left menu open **Casos de uso**, find **WhatsApp** and click
   **Personalizar**.
4. You land on **Configuración de la API**. Copy these two values:
   - **Token de acceso temporal** (access token)
   - **Identificador del número de teléfono** (phone number id), under
     "De" / "From"

> Picture coming soon - waiting for a real screenshot of this page.
> (Issue: we only publish real captures, no mockups.)

> The temporary token lasts 24 hours. When it expires, come back to this
> page and copy the new one into fiveagent.yml. A permanent token needs a
> "system user" in Meta Business settings - that is optional homework for
> later, not needed for your first test.

## Step 2 - Give your PC a public address (cloudflared)

FiveAgent listens on your PC, but Meta's servers need a public address to
deliver messages. cloudflared makes one in seconds, no account needed:

1. Download cloudflared: https://github.com/cloudflare/cloudflared/releases
   (Windows: `cloudflared-windows-amd64.msi`).
2. Start FiveAgent first (it listens on port 8080), then run:

   ```
   cloudflared tunnel --url http://localhost:8080
   ```

3. The terminal shows a public URL like
   `https://something-random.trycloudflare.com`. Copy it.

> **Important:** this URL changes every time you restart cloudflared. If
> you restart it, you must repeat Step 3 with the new URL. Keep the
> cloudflared window open while you use the bot.

> Picture coming soon - waiting for a real screenshot of the cloudflared
> terminal showing the URL.

## Step 3 - Tell Meta where to deliver your messages (Webhook)

1. Back in the Meta app, left menu: **Casos de uso** → WhatsApp →
   **Personalizar** → **Configuración**.
2. Scroll to **Webhook** and click **Editar**.
3. Fill in:
   - **URL de devolución de llamada** (callback URL): your cloudflared URL
     **plus** `/webhook/whatsapp`, e.g.
     `https://something-random.trycloudflare.com/webhook/whatsapp`
   - **Token de verificación / Identificador de verificación** (verify
     token): the same value you put in `verify_token` in fiveagent.yml.
     You invent this value yourself; both sides must match exactly.
4. Click **Verificar y guardar**. If FiveAgent and cloudflared are
   running, it saves on the first try.
5. In **Campos del webhook**, subscribe to **messages**.

> Pictures coming soon - waiting for real screenshots of the Webhook
> dialog and the messages subscription.

> Wrong page? If you see "Información básica" with an app id and a secret
> key, you are in the wrong place (and never touch the secret key). Go to
> **Casos de uso** instead:
>
> ![The basic settings page is NOT where the webhook lives](images/whatsapp-meta-basic.png)

## Step 4 - Add yourself as a test recipient

While the app is in test mode, Meta only delivers messages to numbers you
approve:

1. **Configuración de la API** → section **Para** / "To" →
   **Administrar lista de números**.
2. Add your own phone number with country code (e.g. +34 600 123 456).
3. Meta sends you a code on WhatsApp - enter it to confirm.

> Picture coming soon - waiting for a real screenshot of this page.

## Step 5 - Fill fiveagent.yml and start

```yaml
channels:
  whatsapp:
    enabled: true
    access_token: "<temporary token from Step 1>"
    phone_number_id: "<phone number id from Step 1>"
    verify_token: "<a random string you invent>"
    # app_secret: "<Meta app secret>"  # optional: verifies webhook signatures
```

Start FiveAgent (`fiveagent.exe` or `docker compose up`), keep cloudflared
running, and send **hola** from your WhatsApp to the test number (it
appears on the Configuración de la API page). FiveAgent should answer.

---

## What works today

- Webhook verification (`GET`), constant-time token compare.
- Inbound: text, image, audio (voice notes), document, video, sticker,
  location and reaction messages, described to the agent (e.g. `[image: caption]`).
  `DownloadMedia` fetches the actual bytes from Meta when a tool needs them.
- Outbound: text, media by link (`SendMedia`), approved templates for the
  >24 h window (`SendTemplate`), media upload by id (`UploadMedia`).
- Read receipts + typing indicator on incoming messages.
- Replies quote the original message.
- Delivery/read/failed status notifications are logged.
- Optional X-Hub-Signature-256 verification when `app_secret` is set.
- Async processing (we 200 fast, Meta retries on non-200).
- Unit tests: `go test ./internal/channel/`.

## Missing today

- Voice note transcription (needs a speech-to-text tool).
- Some screenshots in this guide - real captures are being collected;
  no mockups are published.
