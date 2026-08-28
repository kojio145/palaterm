@echo off
REM PalaTerm build script (no wails CLI / no npm required)
setlocal
set SCRIPT_DIR=%~dp0
cd /d "%SCRIPT_DIR%"

echo Building PalaTerm.exe ...
go build -trimpath -tags production -ldflags="-H windowsgui -s -w" -o build\bin\PalaTerm.exe .
if errorlevel 1 (
  echo BUILD FAILED
  exit /b 1
)
echo.
echo Done: build\bin\PalaTerm.exe
endlocal
