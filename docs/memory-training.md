# Entrenamiento de memoria con una IA comercial

Guía para usuarios de FiveAgent: sembrar la memoria a largo plazo
(`data/memory/`) con hechos verificados por tu IA comercial (ChatGPT,
Claude, Gemini, Copilot...) para que **tu modelo local los recuerde en
lugar de alucinarlos**. Es el mismo principio del anti-confabulation seed
del repo (`docs/fivetech-domain.md`), pero con el contenido que tú
elijas.

## Cómo funciona la memoria

- `data/memory/*.md`: markdown con front-matter YAML (`aliases`) y los
  hechos como líneas-bullet `- ...`. La carpeta es un repo git: cada
  guardado queda commitado (auditoría).
- El bot **lee el disco en cada turno**: sueltas un fichero nuevo y ya
  se recuerda en la siguiente conversación, sin reiniciar.
- El recall busca términos de tu mensaje (palabras de **3 letras o
  más**) contra los bullets; los alias del front-matter puntúan x3.
  Por eso las palabras clave deben estar en los bullets y en los alias.
  Alias de 2 letras ("ai") no puntúan.

## Ruta A — entrenar con el propio bot (WhatsApp)

Mándale hechos con el prefijo `recuerda:`:

```
recuerda: mi lenguaje favorito para scripts es Python
recuerda: prefiero respuestas cortas y en español
```

El bot los guarda **directamente** en `preferences` (sin depender de
que el modelo decida llamar a la tool `save_memory`: el prefijo se
aplica siempre, incluso si el modelo responde "de acuerdo" sin más).
Para borrar, el prefijo gemelo `olvida:` quita los bullets que
contengan el texto indicado (en los ficheros estándar):

```
olvida: mi lenguaje favorito para scripts es Python
```

Ambos quedan commitados al instante en el repo de `data/memory/` y
disponibles en el siguiente turno.

## Ruta B — entrenar con tu IA comercial

1. Copia la lista de preguntas de abajo.
2. Pega también la **plantilla de formato** en el mismo chat de tu IA
   comercial (ChatGPT/Claude/Gemini/Copilot).
3. Guarda la respuesta como `data/memory/ia-comercial.md`.
4. Commita el histórico: `git -C data/memory add ia-comercial.md && git -C data/memory commit -m "seed: ia comercial"`
5. Verifica (abajo).

### Plantilla de formato (cópiala tal cual a tu IA comercial)

```
Genera un fichero de conocimiento en Markdown con este formato EXACTO:

---
aliases: [openai, chatgpt, claude, gemini, anthropic, meta, llama]
---
# IA comercial

- <hecho duradero en una frase, con las palabras clave dentro de la frase>
- <otro hecho...>

Reglas:
- Solo líneas que empiezan por "- " (cada bullet = un hecho).
- Hechos duraderos (empresas, productos, conceptos). Los datos
  volátiles (precios, versiones, fechas de salida) van con
  "(actualizado a YYYY-MM-DD)" al final.
- Sin inventar: si no lo sabes con seguridad, no lo pongas.
- Una segunda pasada: añade al front-matter todos los alias de 3+
  letras por los que buscarías cada tema (nombres de empresa,
  productos, sinónimos).
```

### Lista de preguntas sobre IA comercial

**Empresas y productos**

1. ¿Qué empresas desarrollan los principales asistentes de IA comercial (ChatGPT, Claude, Gemini, Copilot, Grok)?
2. ¿Quién creó ChatGPT y en qué año se lanzó al público?
3. ¿Quién desarrolla Claude y quiénes fundaron Anthropic?
4. ¿Quién desarrolla Gemini y cómo se llamaba antes?
5. ¿Qué es Copilot en Microsoft y con qué productos se integra?
6. ¿Qué empresa desarrolla Grok y en qué red social se integra?
7. ¿Quién desarrolla DeepSeek y qué modelo estrella tiene?
8. ¿Qué es Llama, quién lo publica y qué significa que sea de pesos abiertos?
9. ¿De qué país es Mistral AI y qué modelos comerciales tiene?
10. ¿Quién es Sam Altman y qué papel juega en OpenAI?
11. ¿Qué relación empresarial hay entre Microsoft y OpenAI, y entre Amazon/Google y Anthropic?
12. ¿Qué es Perplexity y en qué se diferencia de un buscador clásico?

**Modelos y capacidades**

13. ¿Qué diferencia hay entre un modelo de IA abierto y uno cerrado? Da ejemplos de cada tipo.
14. ¿Qué es un modelo de razonamiento (reasoning) y qué asistentes comerciales lo usan?
15. ¿Qué significa que un asistente sea multimodal? Ejemplos de usos.
16. ¿Qué es la ventana de contexto y por qué importa al elegir un asistente?
17. ¿Qué modelos comerciales generan imágenes (DALL·E, Midjourney, Firefly...)? ⚠ volátil
18. ¿Qué modelos comerciales generan vídeo (Sora, Veo, Runway...)? ⚠ volátil
19. ¿Qué es un copiloto de código? Compara GitHub Copilot, Cursor y Claude Code.
20. ¿Qué familias de modelos tiene OpenAI y cómo se llaman sus modelos de razonamiento? ⚠ volátil

**Conceptos y facturación**

21. ¿Qué es un token y cómo se factura el uso de una API de IA?
22. ¿Qué planes de pago suelen ofrecer los asistentes comerciales (gratis, Plus/Pro, Enterprise)? ⚠ volátil
23. ¿Qué son los asistentes personalizados (GPTs, Gems) y para qué sirven?
24. ¿Qué es RAG y por qué ayuda a un modelo local a responder con datos propios?
25. ¿Por qué un modelo local puede no estar al día de eventos recientes?

**Límites y seguridad**

26. ¿Qué son las alucinaciones de un modelo de IA y cómo se reducen?
27. ¿Qué es la fecha de corte de conocimiento (cutoff) de un modelo?
28. ¿Qué datos personales no conviene enviar a una IA comercial?
29. ¿Cómo se evalúan los modelos (MMLU, HumanEval, Chatbot Arena)?
30. ¿Qué es un prompt de inyección y qué es un jailbreak?

⚠ = dato volátil: la plantilla manda a fecharlo; si caduca, edita el
bullet o bórralo con el prefijo `olvida: ...` (solo ficheros estándar) o
a mano en `data/memory/`.

## Verificación

Tras importar, pregúntale a tu bot por WhatsApp, por ejemplo:

- "¿qué es ChatGPT y quién lo hizo?"
- "¿qué diferencia hay entre un modelo abierto y uno cerrado?"

Si la respuesta refleja tus hechos, la memoria está activa. El
histórico queda en `git -C data/memory log`.

## Límites conocidos

- Las tools `save_memory` / `forget_memory` del bot solo apuntan a los
  ficheros estándar (`people`, `preferences`, `workstreams`): los
  ficheros importados como `ia-comercial.md` se editan a mano (y se
  commitan) o se regeneran repitiendo Ruta B.
- El recall puntúa por palabras de 3+ letras: no apoyes la memoria en
  siglas de dos letras.
