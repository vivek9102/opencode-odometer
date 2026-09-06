@echo off
setlocal
cd /d "%~dp0"

echo Stopping any running odometer...
taskkill /IM OpenCode_Odometer.exe /F >nul 2>&1
del "%TEMP%\opencode_odometer.lock" >nul 2>&1
timeout /t 2 /nobreak >nul

echo Installing PyInstaller if needed...
pip install pyinstaller --quiet

echo Building OpenCode_Odometer.exe ...
pyinstaller --onefile --noconsole --clean --name "OpenCode_Odometer" opencode_monitor.py
if errorlevel 1 goto fail

echo Copying price table next to the exe...
copy /Y prices.json dist\prices.json >nul

echo.
echo ====================================================
echo  SUCCESS
echo  dist\OpenCode_Odometer.exe
echo.
echo  State and budget are stored in:
echo    %%LOCALAPPDATA%%\OpenCodeOdometer\
echo.
echo  For auto-start, copy the plugin:
echo    copy plugin\odometer.js "%%USERPROFILE%%\.config\opencode\plugins\"
echo ====================================================
goto end

:fail
echo.
echo BUILD FAILED - see output above.

:end
pause
