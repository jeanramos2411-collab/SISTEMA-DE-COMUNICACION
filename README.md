# Sistema de Comunicacion PTT (Push-to-Talk)

Comunicacion tipo radio por WiFi local entre dispositivos Android, con panel de administracion web.

## Estructura del proyecto

```
SISTEMA DE COMUNICACION/
├── android/          App Android PTT
├── server/           Servidor Windows
│   ├── INICIAR_SERVIDOR.bat
│   ├── empaquetar_para_copia.bat
│   ├── abrir_panel.bat
│   ├── abrir_firewall.bat
│   ├── ver_ip.bat
│   ├── LEEME.txt
│   ├── app/          Codigo Python + panel web
│   ├── data/         Configuracion (config.json)
│   └── runtime/      Python portable + librerias
└── README.md
```

---

## Servidor — inicio rapido

1. Entre en la carpeta **`server`**
2. **`empaquetar_para_copia.bat`** (primera vez, con internet)
3. **`abrir_firewall.bat`** como Administrador
4. **`INICIAR_SERVIDOR.bat`**
5. **`abrir_panel.bat`** → panel en `http://TU-IP:8766/admin`

Clave panel: **admin123** (cambiar en `server/data/config.json`)

---

## Llevar a otra PC

1. Ejecute **`empaquetar_para_copia.bat`**
2. Copie **`server/dist/PTT-Servidor`** completa a la otra maquina
3. Alli: firewall → iniciar servidor → abrir panel
4. En los celulares cambie la IP del servidor

Detalle completo en **`server/LEEME.txt`**

---

## App Android

Carpeta **`android/`** — compilar con Android Studio (v2.1.0+).

Puerto app: **8765** | Panel admin: **8766**
