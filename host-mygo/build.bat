@echo off
setlocal
cd /d "%~dp0"
set CGO_ENABLED=0
go build -trimpath -ldflags "-s -w -H=windowsgui" -o bin/esp32-ai-usage-mygo.exe .
exit /b %errorlevel%
