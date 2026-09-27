# FiveTech domain seed (draft - pending FiveTech review)

Correct starter facts about the FiveTech ecosystem, written after a real
session where the small model invented Harbour syntax and the meaning of
FWH. Feed these into the long-term memory (copy the bullets into
data/memory/preferences.md or a new domain file, or let the bot save
them) so they get recalled instead of confabulated.

Status: DRAFT. Every bullet here must be reviewed by FiveTech before it
is treated as ground truth; the no-humo rule applies to docs too.

## Products

- FWH means FiveWin for Harbour: FiveTech's Windows GUI library for the
  Harbour language.
- FiveTech's products live at fivetechsoft.com.

## Harbour basics (xBase, Clipper-compatible)

- Variables are declared with LOCAL, STATIC or PRIVATE (never `cVar`).
- Assignment uses `:=`, with inline declaration: `LOCAL cNombre := ""`.
- Console output: `? "Hola", cNombre` (new line) and `??` (same line).
- Console input: `ACCEPT "Tu nombre: " TO cNombre`, or the full-screen
  form `@ 5,10 SAY "Nombre:" GET cNombre` followed by `READ`.
- There is no `Input()` function.

## FiveWin basics

- Programs start with `#include "FiveWin.ch"`.
- A window: `DEFINE WINDOW oWnd TITLE "Hola" FROM 5,10 TO 20,60` then
  `ACTIVATE WINDOW oWnd`.
- Quick dialogs: `MsgInfo("texto")`, `MsgStop("error")`,
  `MsgYesNo("¿seguir?")`.
