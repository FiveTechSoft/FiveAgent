@echo off
setlocal
cd /d "%~dp0"
rem UTF-8 console so the Go log lines (Spanish, symbols) print correctly
chcp 65001 >nul

if not exist fiveagent.exe (
  echo [ERROR] fiveagent.exe not found.
  echo Build it first: go build -o fiveagent.exe ./app/fiveagent
  pause
  exit /b 1
)

rem Restart if already running (avoids port 8080 already in use)
taskkill /im fiveagent.exe /f >nul 2>&1

rem Keep Ollama models loaded in RAM (a 27b reload takes over a minute)
rem NOTE: this only applies if THIS script starts Ollama. If Ollama is
rem already running from the Windows tray, close it (clock icon > Quit)
rem and let this script start it.
set OLLAMA_KEEP_ALIVE=-1
tasklist /fi "imagename eq ollama.exe" | find /i "ollama.exe" >nul
if errorlevel 1 (
  echo Starting Ollama...
  start "" ollama serve
  timeout /t 3 >nul
) else (
  echo Ollama already running: KEEP_ALIVE=-1 NOT applied. Quit it from the tray and rerun this script if replies are slow after idle time.
)

if not exist data mkdir data

start "FiveAgent" /b cmd /c "fiveagent.exe > fiveagent.log 2>&1"
timeout /t 3 >nul

echo === fiveagent.log ===
type fiveagent.log 2>nul
echo.
echo FiveAgent started. Tunnel (if needed): cloudflared tunnel --url http://localhost:8080
rem Pause only on a real console: with redirected stdin (automation,
rem service) plain "pause" fails with "input redirection is not
rem supported". <con gives pause the console keyboard when there is one.
if "%FIVEAGENT_NO_PAUSE%"=="" (
  (pause) <con >nul 2>&1
)
