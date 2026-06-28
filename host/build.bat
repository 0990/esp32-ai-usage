@echo off
REM Fyne 需要 CGO + C 编译器（MinGW-w64 gcc 须在 PATH 上）。
REM -H=windowsgui：去掉运行时的黑色控制台窗口。
set CGO_ENABLED=1
go build -ldflags "-H=windowsgui" -o bin/esp32-ai-credits.exe .
