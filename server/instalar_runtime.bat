@echo off
setlocal enabledelayedexpansion
cd /d "%~dp0"

set "ROOT=%~dp0"
set "RUNTIME=%ROOT%runtime"
set "APP=%ROOT%app"
set "PYVER=3.12.7"
set "PYZIP=python-%PYVER%-embed-amd64.zip"
set "PYURL=https://www.python.org/ftp/python/%PYVER%/%PYZIP%"

if not exist "%RUNTIME%" mkdir "%RUNTIME%"
if not exist "%RUNTIME%\libs" mkdir "%RUNTIME%\libs"

if exist "%RUNTIME%\python\python.exe" (
    "%RUNTIME%\python\python.exe" --version >nul 2>&1
    if not errorlevel 1 (
        if exist "%RUNTIME%\python\python312._pth" (
            if exist "%RUNTIME%\libs\websockets" (
                echo [runtime] Python portable listo.
                exit /b 0
            )
        )
    )
    echo [runtime] Python portable incompleto o corrupto, reinstalando...
)

if exist "%RUNTIME%\python" (
    rmdir /S /Q "%RUNTIME%\python" 2>nul
)
mkdir "%RUNTIME%\python" 2>nul

echo [runtime] Descargando Python %PYVER% embebido...
powershell -NoProfile -ExecutionPolicy Bypass -Command ^
    "try { Invoke-WebRequest -Uri '%PYURL%' -OutFile '%RUNTIME%\%PYZIP%' -UseBasicParsing; exit 0 } catch { Write-Host $_.Exception.Message; exit 1 }"
if errorlevel 1 (
    echo ERROR: No se pudo descargar Python. Revise internet o antivirus.
    exit /b 1
)

echo [runtime] Extrayendo Python...
powershell -NoProfile -ExecutionPolicy Bypass -Command ^
    "try { Expand-Archive -Force '%RUNTIME%\%PYZIP%' '%RUNTIME%\python'; exit 0 } catch { Write-Host $_.Exception.Message; exit 1 }"
del "%RUNTIME%\%PYZIP%" 2>nul
if errorlevel 1 (
    echo ERROR: No se pudo extraer Python.
    exit /b 1
)

if not exist "%RUNTIME%\python\python.exe" (
    echo ERROR: Tras extraer, no existe runtime\python\python.exe
    echo Revise si el antivirus bloqueo el archivo.
    exit /b 1
)

echo [runtime] Configurando python312._pth ...
(
    echo python312.zip
    echo .
    echo ..\libs
    echo import site
) > "%RUNTIME%\python\python312._pth"

echo [runtime] Instalando pip...
powershell -NoProfile -ExecutionPolicy Bypass -Command ^
    "try { Invoke-WebRequest -Uri 'https://bootstrap.pypa.io/get-pip.py' -OutFile '%RUNTIME%\get-pip.py' -UseBasicParsing; exit 0 } catch { exit 1 }"
if errorlevel 1 (
    echo ERROR: No se pudo descargar get-pip.py
    exit /b 1
)

"%RUNTIME%\python\python.exe" "%RUNTIME%\get-pip.py" --no-warn-script-location
if errorlevel 1 (
    echo ERROR: Fallo la instalacion de pip.
    exit /b 1
)
del "%RUNTIME%\get-pip.py" 2>nul

echo [runtime] Instalando dependencias del servidor...
"%RUNTIME%\python\python.exe" -m pip install -r "%APP%\requirements.txt" -t "%RUNTIME%\libs" --upgrade --no-warn-script-location
if errorlevel 1 (
    echo ERROR: Fallo pip install de websockets/aiohttp.
    exit /b 1
)

"%RUNTIME%\python\python.exe" --version
echo [runtime] Instalacion completada.
exit /b 0
