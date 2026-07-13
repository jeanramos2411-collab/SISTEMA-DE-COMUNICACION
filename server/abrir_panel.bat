@echo off
title Abrir panel PTT
cd /d "%~dp0"

echo ========================================
echo   Panel de administracion PTT
echo ========================================
echo.

for /f "tokens=2 delims=:" %%a in ('ipconfig ^| findstr /c:"IPv4"') do (
    set "IP=%%a"
    goto :gotip
)

:gotip
set "IP=%IP: =%"

if "%IP%"=="" (
    echo No se pudo detectar la IP. Ejecute ver_ip.bat
    pause
    exit /b 1
)

echo IP de este PC: %IP%
echo.
echo Panel admin:  http://%IP%:8766/admin
echo App Android:  %IP%  puerto 8765
echo Clave panel:  admin123
echo.
echo IMPORTANTE: El servidor debe estar corriendo (start.bat)
echo.

start "" "http://%IP%:8766/admin"
echo Se abrio el navegador.
pause
