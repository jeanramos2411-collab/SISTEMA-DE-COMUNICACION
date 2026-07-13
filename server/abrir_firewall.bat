@echo off
echo Abriendo reglas de firewall para PTT...
netsh advfirewall firewall add rule name="PTT Comunicacion" dir=in action=allow protocol=TCP localport=8765
netsh advfirewall firewall add rule name="PTT Panel Admin" dir=in action=allow protocol=TCP localport=8766
echo.
echo Reglas agregadas (8765 app, 8766 panel). Si falla, ejecute como Administrador.
pause
