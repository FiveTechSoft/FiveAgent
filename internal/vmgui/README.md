# vmgui - prueba de concepto de la etapa 26 (escritorio virtual)

**Qué es.** Un paquete Go pequeño que controla una pantalla virtual real:
captura de pantalla (`Capture`), clic (`Click`), escritura (`Type`) y
combinaciones de teclas (`Key`). Es la superficie de herramientas que el
ROADMAP (etapa 26, "VM GUI") plantea para el modelo.

**Qué NO es.** No es una máquina virtual. Usa **Xvfb** (un servidor X
virtual en memoria) con `xdotool` e ImageMagick. No hay QEMU, ni KVM, ni
sistema operativo invitado. El backend QEMU/VNC del roadmap sigue siendo
el camino de producción; esta PoC solo demuestra la interfaz y el bucle
ver-actuar-verificar con píxeles reales y eventos reales.

**Qué se probó.** `TestX11DrivesRealAppOnVirtualScreen` levanta Xvfb,
abre gedit, enfoca la ventana, hace clic, escribe un texto y lo verifica
de tres formas: foco de entrada, lectura del portapapeles tras Ctrl+A y
Ctrl+C, y diferencia de píxeles entre capturas antes y después (la
captura "antes" espera a que la pantalla se estabilice).

**Límites.**
- No es una VM: sin aislamiento de archivos, procesos ni red respecto al
  anfitrión.
- Sin árbol de accesibilidad (AT-SPI): actúa por coordenadas.
- No está cableado como herramienta del agente (`main.go` intacto).
- Solo probado en Linux. Windows y macOS siguen abiertos.
- Sin gestor de ventanas: el foco se da con XSetInputFocus.

**Cómo probarlo (Linux).** Requiere Xvfb, xdotool, ImageMagick (`import`),
gedit, xclip y xdpyinfo. Si falta alguno, el test se salta.

    VMGUI_ARTIFACTS=/tmp/vmgui-artifacts go test -count=1 -v ./internal/vmgui/

Guarda `before.png` y `after.png` en esa carpeta.
