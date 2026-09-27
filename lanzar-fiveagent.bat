@echo off
setlocal
cd /d "%~dp0"

if not exist fiveagent.exe (
  echo [ERROR] fiveagent.exe not found.
  echo Build it first: go build -o fiveagent.exe ./app/fiveagent
  pause
  exit /b 1
)

rem Restart if already running (avoids port 8080 already in use)
taskkill /im fiveagent.exe /f >nul 2>&1

if not exist data mkdir data

start "FiveAgent" /b cmd /c "fiveagent.exe > fa_out.log 2> fa_err.log"
timeout /t 3 >nul

echo === fa_err.log ===
type fa_err.log 2>nul
echo.
echo FiveAgent started. Tunnel (if needed): cloudflared tunnel --url http://localhost:8080
pause
