# WhatsApp channel (official Cloud API)

FiveAgent's WhatsApp adapter uses Meta's official WhatsApp Cloud API: no ban
risk, no QR, works for real businesses and personal projects with a Meta app.

## What you need

1. A Meta developer account: https://developers.facebook.com
2. An app with the **WhatsApp** product added.
3. A phone number registered in the app (Meta gives you a free test number
   while you develop).

## Setup

1. In the app dashboard, open **WhatsApp > Configuration** and note:
   - **Phone number ID** (under the test number / your number).
   - A **permanent access token**: create a system user in Meta Business
     Settings, assign the app, and generate a token with
     `whatsapp_business_messaging` (and `whatsapp_business_management`).
     The temporary token from the dashboard expires in 24 h - use the system
     user one for anything real.
2. Choose a random **verify token** yourself (any string).
3. In `fiveagent.yml`:

   ```yaml
   channels:
     whatsapp:
       enabled: true
       access_token: ${WHATSAPP_ACCESS_TOKEN}
       phone_number_id: "1234567890"
       verify_token: "the-random-string-you-picked"
       listen_addr: ":8080"
   ```

4. Start FiveAgent, make the webhook reachable from the internet (a tunnel
   like `cloudflared` or `ngrok` is fine for testing), and in the app
   dashboard set the webhook:

   - **Callback URL:** `https://YOUR-HOST/webhook/whatsapp`
   - **Verify token:** the same string as above.
   - Subscribe to the **messages** field.

5. Send a WhatsApp message to the number. With the Meta test number you must
   add your recipient number to the allowed list first.

## How it works

- Meta verifies the webhook with a GET handshake (`hub.verify_token`).
- Inbound messages arrive as POSTs; FiveAgent answers 200 immediately and
  processes them in the background (Meta retries on non-200).
- Replies go out through `POST /v21.0/{phone_number_id}/messages` on the
  Graph API.
- Non-text messages are ignored for now (v0).
