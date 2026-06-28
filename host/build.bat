@echo off
REM Fyne 需要 CGO + C 编译器（MinGW-w64 gcc 须在 PATH 上）。
REM -H=windowsgui：去掉运行时的黑色控制台窗口。
REM -s -w：去符号表 + DWARF 调试信息，发布版瘦身（~43MB→~30MB），无运行期副作用。
set CGO_ENABLED=1
go build -ldflags "-s -w -H=windowsgui" -o bin/esp32-ai-usage.exe .
