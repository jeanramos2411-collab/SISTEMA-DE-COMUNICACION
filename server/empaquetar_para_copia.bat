@echo off
setlocal enabledelayedexpansion
cd /d "%~dp0"

set "ROOT=%~dp0"
set "DIST=%ROOT%dist\PTT-Servidor"

echo ========================================
echo   Empaquetar servidor PTT para copiar
echo ========================================
echo.
echo Esto prepara Python portable y crea: dist\PTT-Servidor
echo Lista para copiar a otra PC por USB o red.
echo.
pause

echo [1/3] Preparando Python y librerias...
call "%ROOT%instalar_runtime.bat"
if errorlevel 1 (
    echo ERROR: No se pudo preparar runtime.
    pause
    exit /b 1
)

echo.
echo [2/3] Copiando archivos al paquete ...
if exist "%DIST%" rmdir /S /Q "%DIST%"
mkdir "%DIST%"
mkdir "%DIST%\app"
mkdir "%DIST%\data"
mkdir "%DIST%\runtime"

xcopy /E /I /Y "%ROOT%app" "%DIST%\app" >nul
xcopy /E /I /Y "%ROOT%data" "%DIST%\data" >nul
xcopy /E /I /Y "%ROOT%runtime\python" "%DIST%\runtime\python" >nul
xcopy /E /I /Y "%ROOT%runtime\libs" "%DIST%\runtime\libs" >nul

copy /Y "%ROOT%INICIAR_SERVIDOR.bat" "%DIST%\" >nul
copy /Y "%ROOT%instalar_runtime.bat" "%DIST%\" >nul
copy /Y "%ROOT%start.bat" "%DIST%\" >nul
copy /Y "%ROOT%abrir_panel.bat" "%DIST%\" >nul
copy /Y "%ROOT%abrir_firewall.bat" "%DIST%\" >nul
copy /Y "%ROOT%ver_ip.bat" "%DIST%\" >nul
copy /Y "%ROOT%LEEME.txt" "%DIST%\" >nul

echo [3/3] Listo.
echo.
echo Paquete creado en:
echo   %DIST%
echo.
echo Copie esa carpeta completa a la otra PC.
echo Alli: abrir_firewall.bat ^(admin^) y luego INICIAR_SERVIDOR.bat
echo.
pause
