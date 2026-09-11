@echo off
setlocal
cd /d "%~dp0"

echo Stopping any running odometer...
taskkill /IM OpenCode_Odometer.exe /F >nul 2>&1
del "%TEMP%\opencode_odometer.lock" >nul 2>&1
timeout /t 1 /nobreak >nul

rem Sync the embedded copies. //go:embed cannot reach outside its own package
rem directory, so the plugin and the seed price table are duplicated into
rem internal\. A stale copy would be self-installed over a good one.
echo Syncing embedded assets...
copy /Y plugin\odometer.js internal\app\odometer_plugin.js >nul
copy /Y prices.json internal\prices\seed_prices.json >nul

rem Regenerate the app icon so build\appicon.png and build\windows\icon.ico
rem cannot drift from tools\make_icon.ps1.
if exist "tools\make_icon.ps1" (
    echo Generating application icon...
    powershell -NoProfile -ExecutionPolicy Bypass -File "tools\make_icon.ps1" >nul 2>&1
    if errorlevel 1 echo   WARNING: icon generation failed, using the existing icon.
)

rem A plain 'go build' produces a working binary with NO icon resource, because
rem Wails compiles build\windows\icon.ico in during its own build step. Falling
rem back silently is how a build ends up showing the default window icon, so
rem require the CLI rather than degrading.
where wails >nul 2>&1
if errorlevel 1 (
    echo.
    echo ERROR: the Wails CLI is not on PATH.
    echo   Install it with:
    echo     go install github.com/wailsapp/wails/v2/cmd/wails@latest
    echo   then ensure %%USERPROFILE%%\go\bin is on PATH.
    goto fail
)

echo Building OpenCode_Odometer.exe with Wails...
wails build
if errorlevel 1 goto fail

echo Copying price table next to the binary...
if not exist "build\bin" mkdir "build\bin"
copy /Y prices.json build\bin\prices.json >nul
if exist prices.local.json copy /Y prices.local.json build\bin\prices.local.json >nul

rem The plugin publishes the OpenCode server URL that the odometer discovers,
rem so an out-of-date copy leaves the odometer unable to find the server.
echo Installing the OpenCode plugin...
set "PLUGIN_DIR=%USERPROFILE%\.config\opencode\plugins"
if not exist "%PLUGIN_DIR%" mkdir "%PLUGIN_DIR%"
copy /Y plugin\odometer.js "%PLUGIN_DIR%\odometer.js" >nul
if errorlevel 1 (
    echo   WARNING: could not install the plugin to %PLUGIN_DIR%
) else (
    echo   Installed to %PLUGIN_DIR%
)

echo.
echo ====================================================
echo  SUCCESS!
echo  Binary built: build\bin\OpenCode_Odometer.exe
echo.
echo  State and budget are stored in:
echo    %%LOCALAPPDATA%%\OpenCodeOdometer\
echo.
echo  The plugin starts the odometer automatically with
echo  OpenCode. Restart OpenCode to pick up changes.
echo ====================================================
goto end

:fail
echo.
echo BUILD FAILED - see output above.

:end
pause
