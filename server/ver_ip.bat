@echo off
setlocal enabledelayedexpansion
echo ========================================
echo   IP local del servidor Windows
echo ========================================
echo.

set "FOUND=0"
for /f "tokens=2 delims=:" %%a in ('ipconfig ^| findstr /c:"IPv4"') do (
    set "IP=%%a"
    set "IP=!IP: =!"
    echo   IP: !IP!
    echo   App Android ........... !IP!  ^(puerto 8765^)
    echo   Panel administracion .. http://!IP!:8766/admin
    echo   Clave del panel ....... admin123
    echo.
    set "FOUND=1"
)

if "!FOUND!"=="0" (
    echo   No se encontro IPv4. Revise su conexion WiFi/Ethernet.
)

echo ========================================
pause
