@echo off
setlocal
cd /d "%~dp0app"
if errorlevel 1 goto fail

where go >nul 2>nul
if errorlevel 1 (
  echo Go was not found. Install Go 1.24 or newer from https://go.dev/dl and run this again.
  goto fail
)

set "DEST=%USERPROFILE%\lilDocker\bin"
if not exist "%DEST%" mkdir "%DEST%"

echo Building lilDocker...
go build -o "%DEST%\lilDocker.exe" .\cmd
if errorlevel 1 goto fail

powershell -NoProfile -ExecutionPolicy Bypass -Command "$d = '%DEST%'; $p = [Environment]::GetEnvironmentVariable('Path','User'); if (-not $p) { $p = '' }; if (($p -split ';') -notcontains $d) { [Environment]::SetEnvironmentVariable('Path', (($p.TrimEnd(';') + ';' + $d).TrimStart(';')), 'User') }"

echo.
echo Installed: %DEST%\lilDocker.exe
echo Close this window and open a new Command Prompt or PowerShell, then run:
echo.
echo     lilDocker version
echo.
exit /b 0

:fail
echo.
echo Install failed.
exit /b 1
