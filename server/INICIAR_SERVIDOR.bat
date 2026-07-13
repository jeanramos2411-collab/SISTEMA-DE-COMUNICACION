@echo off
setlocal enabledelayedexpansion
title PTT Comunicacion - Servidor
cd /d "%~dp0"

set "ROOT=%~dp0"
set "APP=%ROOT%app"
set "RUNTIME=%ROOT%runtime"
set "DATA=%ROOT%data"
set "PYTHON="
set "PYMODE="

echo ========================================
echo   PTT Comunicacion - Servidor WiFi
echo ========================================
echo.

if not exist "%APP%\main.py" (
    set "FAILMSG=No se encontro app\main.py. Ejecute este bat desde la carpeta server."
    goto :fail
)

call :ensure_portable
if errorlevel 1 call :try_system_python
if errorlevel 1 (
    set "FAILMSG=No hay Python usable. Ejecute instalar_runtime.bat o instale Python 3.10+."
    goto :fail
)

call :show_info

if not exist "%DATA%" mkdir "%DATA%"

echo Iniciando servidor... ^(Ctrl+C para detener^)
echo.
cd /d "%APP%"
set "PYTHONPATH=%APP%;%RUNTIME%\libs"
"%PYTHON%" main.py
set "EXITCODE=!ERRORLEVEL!"
cd /d "%ROOT%"

echo.
if not "!EXITCODE!"=="0" (
    set "FAILMSG=El servidor termino con error !EXITCODE!. Revise los mensajes arriba."
    goto :fail
)

echo Servidor detenido correctamente.
echo.
pause
exit /b 0

:ensure_portable
if not exist "%RUNTIME%\python\python.exe" goto :ep_missing
"%RUNTIME%\python\python.exe" --version >nul 2>&1
if errorlevel 1 goto :ep_missing
if not exist "%RUNTIME%\python\python312._pth" goto :ep_missing
if not exist "%RUNTIME%\libs\websockets" goto :ep_missing
set "PYTHON=%RUNTIME%\python\python.exe"
set "PYMODE=portable"
set "PYTHONPATH=%APP%;%RUNTIME%\libs"
exit /b 0

:ep_missing
echo Python portable no encontrado o incompleto.
echo Reparando runtime automaticamente ^(requiere internet la primera vez^)...
echo.
call "%ROOT%instalar_runtime.bat"
if errorlevel 1 exit /b 1
set "PYTHON=%RUNTIME%\python\python.exe"
set "PYMODE=portable"
set "PYTHONPATH=%APP%;%RUNTIME%\libs"
exit /b 0

:try_system_python
echo.
echo Intentando Python del sistema ^(modo desarrollo^)...

set "SYSPY="
where python >nul 2>&1
if not errorlevel 1 (
    for /f "delims=" %%i in ('where python 2^>nul ^| findstr /v /i "WindowsApps"') do (
        set "SYSPY=%%i"
        goto :sys_found
    )
)

if exist "%LOCALAPPDATA%\Programs\Python\Python312\python.exe" (
    set "SYSPY=%LOCALAPPDATA%\Programs\Python\Python312\python.exe"
    goto :sys_found
)
if exist "%LOCALAPPDATA%\Programs\Python\Python313\python.exe" (
    set "SYSPY=%LOCALAPPDATA%\Programs\Python\Python313\python.exe"
    goto :sys_found
)

exit /b 1

:sys_found
echo Usando: !SYSPY!
if not exist "%RUNTIME%\venv\Scripts\python.exe" (
    echo Creando entorno virtual en runtime\venv ...
    "!SYSPY!" -m venv "%RUNTIME%\venv"
    if errorlevel 1 exit /b 1
)

set "PYTHON=%RUNTIME%\venv\Scripts\python.exe"
if not exist "!PYTHON!" exit /b 1

echo Instalando dependencias en venv...
"!PYTHON!" -m pip install -r "%APP%\requirements.txt" -q
if errorlevel 1 exit /b 1

set "PYMODE=dev"
set "PYTHONPATH=%APP%"
exit /b 0

:show_info
"%PYTHON%" --version
if errorlevel 1 (
    set "FAILMSG=Python no responde: %PYTHON%"
    goto :fail
)

if "%PYMODE%"=="portable" echo Modo: PORTABLE ^(sin instalar Python en Windows^)
if "%PYMODE%"=="dev" echo Modo: DESARROLLO ^(venv del sistema^)
echo.
echo Puertos: 8765 ^(app Android^)  8766 ^(panel admin^)
echo.

set "SVRIP="
for /f "tokens=2 delims=:" %%a in ('ipconfig ^| findstr /c:"IPv4"') do (
    set "SVRIP=%%a"
    goto :got_ip
)
:got_ip
if defined SVRIP set "SVRIP=!SVRIP: =!"
if defined SVRIP (
    echo IP servidor: !SVRIP!
    echo Panel admin: http://!SVRIP!:8766/admin
) else (
    echo Ejecute ver_ip.bat para ver la IP de este PC.
)
echo.
exit /b 0

:fail
echo.
echo ========================================
echo   ERROR
echo ========================================
echo !FAILMSG!
echo.
echo Soluciones:
echo   1. Ejecute instalar_runtime.bat ^(con internet^)
echo   2. Ejecute empaquetar_para_copia.bat
echo   3. Revise antivirus / cuarentena de python.exe
echo   4. Instale Python 3.10+ desde python.org
echo.
pause
exit /b 1
