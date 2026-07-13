@echo off
REM Script para compilar el servidor PTT en Go
echo Compilando servidor PTT...

cd /d "%~dp0"

REM Limpiar builds anteriores
if exist "ptt-server.exe" del "ptt-server.exe"

REM Compilar
go build -o ptt-server.exe ./cmd/ptt-server

if %ERRORLEVEL% EQU 0 (
    echo.
    echo Compilacion exitosa!
    echo.
    echo Para ejecutar el servidor:
    echo   ptt-server.exe
    echo.
    echo Panel admin: http://localhost:8766/admin
    echo Clave admin: admin123
) else (
    echo.
    echo Error en la compilacion.
    echo Asegurate de tener Go instalado.
)

pause
