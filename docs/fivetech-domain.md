# FiveTech domain seed

Correct starter facts about the FiveTech ecosystem, written after a real
session where the small model invented Harbour syntax and the meaning of
FWH. Feed these into the long-term memory (copy the bullets into
data/memory/preferences.md or a new domain file, or let the bot save
them) so they get recalled instead of confabulated.

Status: verified against public sources on 2026-09-28. Sources: the
official FiveWin for Harbour documentation (fivetechsoft.github.io/FWH_docs),
fivetechsoft.com, and the CA-Clipper 5.3 guide mirrored at
harbour.github.io/ng. Anything FiveTech wants to add beyond these
public facts stays marked "pending FiveTech confirmation".

## Products

- FWH means FiveWin for Harbour: FiveTech's Windows application
  development framework (GUI class library + xBase commands) for the
  Harbour and xHarbour languages, on top of the Win32/Win64 API.
  Source: fivetechsoft.github.io/FWH_docs/en/getting-started/overview.html
- FiveTech's products live at fivetechsoft.com; the official FWH
  documentation lives at fivetechsoft.github.io/FWH_docs.

## Harbour basics (xBase, Clipper-compatible)

- Variables are declared with LOCAL, STATIC or PRIVATE (never `cVar` -
  that name was a confabulation).
- Assignment uses `:=`, with inline declaration: `LOCAL cNombre := ""`.
- Console output: `? "Hola", cNombre` (new line) and `??` (same line).
- Console input: `ACCEPT "Tu nombre: " TO cNombre`, or the full-screen
  form `@ 5,10 SAY "Nombre:" GET cNombre` followed by `READ`.
- There is no `Input()` function in Harbour - that name was a
  confabulation born in the same session.

## FiveWin basics

- Programs start with `#include "FiveWin.ch"`.
- A window (official quick-start form):
  `DEFINE WINDOW oWnd TITLE "Hola" SIZE 600, 400` then
  `ACTIVATE WINDOW oWnd CENTERED`.
- Dialogs use the same xBase form commands with pixel sizes:
  `@ 20, 20 SAY "Name:" OF oDlg SIZE 60, 22 PIXEL`,
  `@ 20, 90 GET oGet VAR cName OF oDlg SIZE 300, 24 PIXEL`.
- Quick dialogs: `MsgInfo("texto")`, `MsgStop("error")`,
  `MsgYesNo("¿seguir?")`.
