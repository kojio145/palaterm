@echo off
REM PalaTerm build script (no wails CLI / no npm required)
setlocal
set SCRIPT_DIR=%~dp0
cd /d "%SCRIPT_DIR%"

echo Building PalaTerm.exe ...
REM The exe is built with a copy of Wails that has one function changed
REM (tools\mkwails.go has the why; docs\BUILD.md the steps).
go run tools\mkwails.go
if errorlevel 1 (
  echo BUILD FAILED
  exit /b 1
)
go build -modfile build/release.mod -trimpath -buildvcs=false -tags production -ldflags="-H windowsgui -s -w" -o build\bin\PalaTerm.exe .
if errorlevel 1 (
  echo BUILD FAILED
  exit /b 1
)
echo.
echo Done: build\bin\PalaTerm.exe
endlocal
