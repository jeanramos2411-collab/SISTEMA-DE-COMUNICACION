@echo off
chcp 65001 >nul
setlocal

echo ========================================
echo   Importar Config del Servidor Python
echo ========================================
echo.

cd /d "%~dp0"

REM Verificar que existe el directorio del servidor Python
if not exist "..\server\data\config.json" (
    echo ERROR: No se encontro config.json en el servidor Python
    echo Ubicacion esperada: ..\server\data\config.json
    echo.
    echo Asegurate de que la estructura de carpetas sea:
    echo   SISTEMA-DE-COMUNICACION\
    echo     +- server\         ^server-go\
    echo.
    pause
    exit /b 1
)

REM Crear directorio data si no existe
if not exist "data" (
    mkdir data
    echo [OK] Directorio data creado.
)

REM Copiar config.json
echo.
echo Copiando configuracion...
copy /Y "..\server\data\config.json" "data\config.json"

if %ERRORLEVEL% EQU 0 (
    echo.
    echo ========================================
    echo   Configuracion importada exitosamente!
    echo ========================================
    echo.
    echo Canales importados:
    findstr /C:"\"name\":" "data\config.json" | findstr /V "approved_channel" | findstr /V "channel_id"
    echo.
    echo Ahora puedes iniciar el servidor Go:
    echo   INICIAR_SERVIDOR.bat
) else (
    echo.
    echo ERROR al copiar la configuracion.
)

echo.
pause
