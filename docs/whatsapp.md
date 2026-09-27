# WhatsApp con FiveAgent: guía para principiantes

Esta guía te lleva de cero a hablar con FiveAgent por WhatsApp. No hace
falta saber programar. Cada paso tiene **una sola acción**, su captura
real y un "qué deberías ver". Todas las capturas son reales (con los
datos privados tapados) y las trampas descritas nos pasaron de verdad en
una instalación real el 27-sep-2026.

**English:** beginner WhatsApp setup guide. The steps are in simple
Spanish; the Meta panel button names are quoted so you can follow them
in any language.

## Cómo funciona (el dibujo)

```
tu móvil  -->  Meta (WhatsApp)  -->  túnel cloudflared  -->  tu PC (FiveAgent)
                                                             |
                                                             v
                                                          el modelo
                                                       (Ollama o DeepSeek)
                                                             |
tu móvil  <--  Meta (WhatsApp)  <--  túnel cloudflared  <--  respuesta
```

Tu móvil escribe al número de prueba. Meta avisa a tu PC a través del
túnel. FiveAgent piensa la respuesta con el modelo y la devuelve por el
mismo camino.

Dos palabras que salen mucho:

- **token**: una contraseña larga que Meta te da para que tu programa
  pueda hablar con su API. Caduca en 24 horas (la versión para empezar).
- **webhook**: la dirección pública donde Meta entrega los mensajes que
  te escriben. Es la URL del túnel más `/webhook/whatsapp`.

## Lo que necesitas

- Un PC con Windows y conexión a internet.
- Una cuenta de Facebook (para crear la app de Meta, gratis).
- Tu móvil con WhatsApp.
- Unos 30 minutos.

---

# Parte 1: tu PC (el "server")

## Paso 1 - Descarga y compila FiveAgent

Instala Go (https://go.dev/dl/, el instalador para Windows, siguiente-
siguiente) y Git (https://git-scm.com/download/win). Abre PowerShell:

```
git clone https://github.com/FiveTechSoft/FiveAgent
cd FiveAgent
go build -o fiveagent.exe ./app
```

**Qué deberías ver:** un archivo `fiveagent.exe` en la carpeta. (De
momento se compila así; cuando haya descargas listas lo diremos aquí.)

## Paso 2 - Crea tu fiveagent.yml

Crea un archivo `fiveagent.yml` al lado de `fiveagent.exe` con este
contenido (de momento deja los valores de WhatsApp vacíos, los
rellenarás en el Paso 8):

El modelo es quien redacta las respuestas. Tienes dos opciones;
rellena **una** de las dos:

Opción A - **DeepSeek** (nube, es con la que hicimos la prueba en vivo
de esta guía; necesita una API key de https://platform.deepseek.com):

```yaml
model:
  provider: openai-compatible
  base_url: "https://api.deepseek.com/v1"
  api_key: "<tu clave de DeepSeek>"
  name: "deepseek-chat"

channels:
  whatsapp:
    enabled: true
    access_token: ""          # Paso 8
    phone_number_id: ""       # Paso 8
    verify_token: "inventa-una-palabra-larga-y-rara"
```

Opción B - **Ollama** (gratis, todo en tu PC, sin nube; todavía no lo
hemos probado en vivo - si lo pruebas, cuéntanos):

```yaml
model:
  provider: openai-compatible
  base_url: "http://localhost:11434/v1"
  api_key: ""                 # con Ollama va vacío
  name: "qwen3"               # el modelo que hayas descargado

channels:
  whatsapp:
    enabled: true
    access_token: ""          # Paso 8
    phone_number_id: ""       # Paso 8
    verify_token: "inventa-una-palabra-larga-y-rara"
```

Para Ollama: instálalo de https://ollama.com y descarga un modelo con
`ollama pull qwen3` (u otro). Déjalo corriendo; FiveAgent habla con él
en tu propio PC.

- `verify_token`: invéntatelo tú. Lo usarás dos veces: aquí y en el
  panel de Meta. Sirve para que Meta compruebe que habla contigo.

## Paso 3 - Arranca FiveAgent

```
.\fiveagent.exe
```

**Qué deberías ver:** una línea que dice que está escuchando, algo como
`whatsapp webhook listening on :8080/webhook/whatsapp`. Significa: "tu
PC espera mensajes de Meta en el puerto 8080". Deja esta ventana
abierta. Si algo falla, mira el archivo `fa_err.log` en la misma
carpeta: ahí se apuntan los errores y cada mensaje que llega.

---

# Parte 2: la app de Meta

## Paso 4 - Entra en tu app de Meta

Entra en https://developers.facebook.com/apps e inicia sesión. Si no
tienes app: **Crear aplicación** → **Otro** → **Empresa** y ponle el
nombre que quieras. Si ya tienes una, haz clic en ella:

![Mis aplicaciones: entra en tu app](images/2-meta-mis-aplicaciones.png)

**Qué deberías ver:** tu app en la lista, como en la captura.

## Paso 5 - Abre el caso de uso de WhatsApp

En el menú de la izquierda: **Casos de uso**. En la tarjeta de
WhatsApp, pulsa **Personalizar**:

![Casos de uso: Personalizar](images/3-meta-casos-de-uso.png)

**Qué deberías ver:** la pantalla "Personaliza el caso de uso".

## Paso 6 - Ve a "Paso 1. Probar"

En el menú de la izquierda, dentro de **Configuración básica**, pulsa
**Paso 1. Probar...**:

![Menú: Paso 1. Probar](images/4-meta-personalizar-paso1.png)

**Qué deberías ver:** la sección "Solicita un número de prueba de
WhatsApp".

## Paso 7 - Genera el número de prueba y apunta 3 cosas

Pulsa **Generar identificador**. Meta crea tu número de prueba y tu
primer token:

![Paso 1: número de prueba, IDs y token](images/5-meta-paso1-probar.png)

Apunta en un papel (o directamente en tu `fiveagent.yml`, Paso 8):

1. el **token de acceso** (la contraseña larga),
2. el **Phone Number ID**,
3. el **número de prueba** (a ese número escribirás desde tu móvil).

**Qué deberías ver:** los campos rellenos donde la captura tiene los
huecos en blanco (en la captura están tapados, son datos privados).

> Ojo: este token **caduca en 24 horas**. Si mañana el bot no envía,
> vuelve aquí, copia el token nuevo y ponlo en `fiveagent.yml`.

## Paso 8 - Rellena fiveagent.yml

Vuelve a tu `fiveagent.yml` y rellena:

- `access_token`: el token del Paso 7.
- `phone_number_id`: el Phone Number ID del Paso 7.

Reinicia FiveAgent (cierra la ventana y vuelve a ejecutar
`.\fiveagent.exe`).

**Qué deberías ver:** otra vez la línea de "listening". Sin errores en
`fa_err.log`.

## Paso 9 - Autoriza tu número de móvil

Mientras la app está en modo prueba, Meta **solo** entrega mensajes a
los números que autorices. En la misma página del Paso 7, baja a
**Destinatario** y pulsa **Administrar lista de números de teléfono**:

![Destinatario: administrar lista](images/6-meta-paso1-destinatario.png)

Añade tu número con prefijo de país (ejemplo: +34 600 123 456). Meta te
envía un código por WhatsApp; escríbelo para confirmar.

**Qué deberías ver:** tu número en la lista, confirmado.

---

# Parte 3: el túnel (una dirección pública para tu PC)

Tu PC está en tu casa y Meta no puede verlo. **cloudflared** crea un
túnel: una dirección pública temporal que lleva a tu PC.

## Paso 10 - Instala y arranca cloudflared

1. Descárgalo: https://github.com/cloudflare/cloudflared/releases
   (Windows: `cloudflared-windows-amd64.msi`). No hace falta cuenta.
2. Abre PowerShell y ejecuta:

   ```
   cloudflared tunnel --url http://localhost:8080
   ```

3. En el texto que sale, busca una línea con una URL tipo
   `https://palabras-al-azar.trycloudflare.com`. Cópiala.

**Qué deberías ver:** la URL del túnel y líneas de "Registered tunnel
connection". Deja esta ventana abierta siempre que uses el bot.

> **Aviso honesto (probado hoy):** este túnel rápido es inestable. En
> nuestras pruebas se cayó 3 veces en unos 25 minutos (cada ~10 minutos)
> con el error `timeout: no recent network activity`: la conexión
> QUIC/UDP muere cuando el router la deja inactiva, y los mensajes que
> llegan mientras tanto se pierden. Estamos probando como mitigación
> arrancarlo con `cloudflared tunnel --url http://localhost:8080
> --protocol http2`; **todavía está en prueba**, actualizaremos esta
> guía cuando confirmemos que aguanta. Para algo serio, mira la sección
> "Para producción" al final.

---

# Parte 4: conectar Meta con tu PC (el webhook)

## Paso 11 - Registra el webhook

En el menú de la izquierda del panel de Meta, dentro de **Configuración
básica**, pulsa **Paso 2. Configuración de producción...**. En la
sección **Configurar Webhooks** rellena:

- **URL de devolución de llamada**: tu URL del túnel **más**
  `/webhook/whatsapp`. Ejemplo:
  `https://palabras-al-azar.trycloudflare.com/webhook/whatsapp`
- **Identificador de verificación**: el mismo `verify_token` que
  inventaste en el Paso 2.

Pulsa **Verificar y guardar**:

![Webhook: URL, token y botón de guardar](images/7-meta-paso2-webhook.png)

**Qué deberías ver:** se guarda sin error (con FiveAgent y cloudflared
encendidos funciona a la primera). En la ventana de FiveAgent aparece
una línea de actividad.

## Paso 12 - Suscríbete al campo "messages"

En la misma página, baja a **Campos de webhook**, busca la fila
**messages** y activa el interruptor hasta que diga **Suscrito**:

![Campos de webhook: messages suscrito](images/8-meta-messages-suscrito.png)

**Qué deberías ver:** la fila `messages` con el interruptor azul y el
texto "Suscrito".

> Esto es la **trampa número 1**: sin este interruptor, el panel de
> Meta muestra eventos pero **nunca envía nada a tu PC**. Nos costó
> horas descubrirlo.

## Paso 13 - Prueba de verdad

Desde tu móvil (el número que autorizaste en el Paso 9), abre WhatsApp
y escribe **Hola** al número de prueba.

**Qué deberías ver:** FiveAgent te responde en WhatsApp en unos
segundos. Y en el registro (`fa_err.log`) líneas como:

```
whatsapp: inbound POST, 555 bytes
whatsapp: message from 346XXXXXXXXX (type text)
```

Esto está probado en vivo, de punta a punta (27-sep-2026):

![Conversación real: FiveAgent responde por WhatsApp](images/1-2-whatsapp-e2e-real.jpg)

---

## Si algo no va

| Síntoma | Causa casi segura |
|---|---|
| En el log no llega NADA al escribir al bot | Paso 12: `messages` no está "Suscrito" |
| Llegan avisos raros pero no tus mensajes | Falta suscribir la app a la WABA (caja avanzada de abajo) |
| "Verificar y guardar" falla | FiveAgent apagado, túnel caído, o verify_token distinto en los dos lados |
| El bot recibe pero no responde | Token de Meta caducado (24 h) o clave del modelo mal |
| Dejó de funcionar al rato | El túnel rápido se cayó (ver Paso 10) o lo reiniciaste y la URL cambió |

## Si reinicias algo

- **cloudflared** → la URL cambia → repite el Paso 11 con la URL nueva.
- **Token caducado** → token nuevo del Paso 7 en `fiveagent.yml` y
  reinicia FiveAgent.

## Página equivocada frecuente

Si en algún momento ves "Información básica" con un "Identificador de
la aplicación" y una "Clave secreta", **no es ahí** (y no toques la
clave secreta). Vuelve a **Casos de uso** → WhatsApp → **Personalizar**:

![La página de información básica NO es donde vive el webhook](images/1-whatsapp-meta-basic.png)

## Caja avanzada: suscribir la app a la WABA (por API)

Solo si el Paso 13 no recibe nada pese a tener `messages` suscrito.
**Trampa número 2** (también real): la app debe estar suscrita a la
cuenta de WhatsApp Business (WABA), y el panel no deja hacerlo - va por
API. Síntoma: en `subscribed_apps` solo aparece la app interna de Meta
("WA DevX Webhook Events 1P App"), no la tuya.

Necesitas tu **WABA id** (aparece en la página del Paso 7 o en la URL
del panel). Con el token del Paso 7:

```
curl -X POST "https://graph.facebook.com/v21.0/<WABA_ID>/subscribed_apps" -H "Authorization: Bearer <TOKEN>"
```

Debe responder `{"success": true}`. Compruébalo con un GET a la misma
URL: tu app tiene que aparecer en la lista.

## Para producción (todavía no probado aquí)

Esto aún **no lo hemos probado**; es el camino documentado por Meta y
Cloudflare, no experiencia propia:

- Un túnel **nombrado** de Cloudflare (URL fija, no cambia al reiniciar)
  o un servidor con dirección fija, en vez del túnel rápido.
- Un token **permanente** de Meta creado con un "usuario del sistema"
  en Meta Business, en vez del token de 24 horas.

## En el futuro

Queremos un `fiveagent setup whatsapp` que haga todo esto con un
comando ([issue #14](https://github.com/FiveTechSoft/FiveAgent/issues/14)).
**Todavía no existe**: hoy el camino es esta guía.

---

## Referencia técnica

### Lo que funciona hoy

- Verificación del webhook (`GET`) con comparación en tiempo constante.
- Entrante: texto, imágenes, notas de voz, documentos, vídeo, stickers,
  ubicaciones y reacciones, descritos al agente (ej.: `[image: caption]`).
  `DownloadMedia` baja los bytes de Meta cuando haga falta.
- Saliente: texto, media por enlace (`SendMedia`), plantillas aprobadas
  para fuera de la ventana de 24 h (`SendTemplate`), subida de media
  (`UploadMedia`).
- Acuse de lectura + indicador de "escribiendo..." al recibir.
- Las respuestas citan el mensaje original.
- Estados de entrega (enviado/entregado/leído/fallido) en el registro.
- Verificación opcional de firma X-Hub-Signature-256 con `app_secret`.
- Procesado asíncrono (200 rápido; Meta reintenta si no).
- Registro de cada POST entrante y cada mensaje en `fa_err.log`.
- Tests: `go test ./internal/channel/`.

### Lo que falta hoy

- Transcribir notas de voz (necesita un servicio de voz a texto).
- Sección de "túnel fijo con tu dominio en Cloudflare" con capturas
  propias: en preparación, se publicará cuando esté probada.
