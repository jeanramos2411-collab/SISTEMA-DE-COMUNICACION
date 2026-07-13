@echo off
chcp 65001 >nul
setlocal

echo ========================================
echo   Servidor PTT Go - Comunicacion WiFi
echo ========================================
echo.

cd /d "%~dp0"

REM Verificar que el ejecutable existe
if not exist "ptt-server.exe" (
    echo ERROR: No se encontro ptt-server.exe
    echo Ejecute compilar.bat primero para compilar el servidor.
    echo.
    pause
    exit /b 1
)

REM Crear directorio data si no existe
if not exist "data" (
    mkdir data
    echo [INFO] Directorio data creado.
)

REM Verificar si existe config.json
if not exist "data\config.json" (
    echo ========================================
    echo   ADVERTENCIA: config.json no encontrado!
    echo ========================================
    echo Se usara configuracion por defecto.
    echo.
    echo Para importar desde el servidor Python, ejecuta:
    echo   IMPORTAR_CONFIG.bat
    echo.
    echo Presiona cualquier tecla para continuar...
    pause >nul
) else (
    echo [OK] config.json encontrado en data\
)

REM Mostrar informacion
echo.
echo Puertos: 8765 (app Android)  8766 (panel admin)
echo.

REM Obtener IP del servidor
for /f "tokens=2 delims=:" %%a in ('ipconfig ^| findstr /i "ipv4" ^| findstr /v "127"') do (
    for /f "tokens=*" %%b in ("%%a") do set "IP=%%b"
)
set "IP=%IP: =%"
if not defined IP set "IP=localhost"

echo IP servidor: %IP%
echo Panel admin: http://%IP%:8766/admin
echo.
echo Iniciando servidor... (Ctrl+C para detener)
echo.

REM Ejecutar el servidor
ptt-server.exe

pause
