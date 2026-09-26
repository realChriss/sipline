@echo off
setlocal
cd /d "%~dp0"
set CGO_ENABLED=0
for %%o in (windows linux darwin) do for %%a in (amd64 arm64) do call :build %%o %%a || exit /b 1
exit /b 0

:build
set GOOS=%1
set GOARCH=%2
set OUT=dist\sipline-%1-%2
if %1==windows set OUT=%OUT%.exe
go build -trimpath -ldflags "-s -w" -o %OUT% . || exit /b 1
echo %OUT%
exit /b 0
