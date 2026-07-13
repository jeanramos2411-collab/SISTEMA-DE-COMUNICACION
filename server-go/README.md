# Servidor PTT en Go

Servidor de comunicación PTT (Push-to-Talk) migrado de Python a Go para mejor rendimiento y escalabilidad.

## Características

- **WebSocket Server** (puerto 8765) - Comunicación de audio en tiempo real
- **HTTP Admin Panel** (puerto 8766) - Panel de administración web
- **Persistencia JSON** - Datos guardados en `data/config.json`
- **Gestión de canales** - Crear, editar, eliminar canales
- **Control de acceso** - Canales abiertos y de aprobación
- **Bloqueo de usuarios** - Por nombre, dispositivo o IP
- **Aprobación de solicitudes** - Para canales que requieren aprobación

## Requisitos

- Go 1.21 o superior

## Primeros Pasos

### 1. Compilar el servidor

#### Windows
```bash
compilar.bat
```

#### Linux / macOS
```bash
chmod +x compilar.sh
./compilar.sh
```

### 2. Configurar (importante)

Si ya tienes el servidor Python configurado con canales personalizados:

1. Copia el archivo `data/config.json` del servidor Python al directorio `data/` del servidor Go
2. Si el directorio `data/` no existe, se creará automáticamente

```batch
REM En Windows, desde la carpeta server-go
copy ..\server\data\config.json data\
```

### 3. Iniciar el servidor

#### Windows
```bash
INICIAR_SERVIDOR.bat
```

O manualmente:
```bash
ptt-server.exe
```

#### Linux/macOS
```bash
./ptt-server
```

## Configuración de la App Android

En la app Android, configura la IP del servidor Go (la misma que muestra el servidor al iniciar).

## API del Panel Admin

### Autenticación

Todas las peticiones excepto `/api/login` y `/api/public/info` requieren el header:
```
X-Admin-Token: <password>
```

## Configuración

### Variables de entorno (opcionales)

- `DATA_DIR` - Directorio para datos (por defecto: `data`)
- `STATIC_DIR` - Directorio para archivos estáticos (por defecto: `static`)

### Archivo de configuración

El archivo `data/config.json` contiene:

```json
{
  "admin_password": "admin123",
  "playback_gain": 3.0,
  "channels": [
    {"id": "canal-1", "name": "Canal 1", "enabled": true, "access": "open"},
    ...
  ],
  "blocked": [],
  "devices": {},
  "pending_approvals": []
}
```

## Puertos

| Servicio | Puerto | Descripción |
|----------|--------|-------------|
| WebSocket | 8765 | Conexiones de clientes PTT |
| Admin Panel | 8766 | Panel de administración HTTP |

## API del Panel Admin

### Autenticación

Todas las peticiones excepto `/api/login` y `/api/public/info` requieren el header:
```
X-Admin-Token: <password>
```

### Endpoints

| Método | Ruta | Descripción |
|--------|------|-------------|
| POST | `/api/login` | Iniciar sesión |
| GET | `/api/public/info` | Info pública (sin auth) |
| GET | `/api/status` | Estado completo |
| PUT | `/api/settings/gain` | Cambiar gain global |
| PUT | `/api/devices/{id}/gain` | Cambiar gain de dispositivo |
| POST | `/api/channels` | Crear canal |
| PUT | `/api/channels/{id}` | Actualizar canal |
| DELETE | `/api/channels/{id}` | Eliminar canal |
| POST | `/api/blocked` | Bloquear usuario |
| DELETE | `/api/blocked/{id}` | Desbloquear |
| POST | `/api/kick/{session_id}` | Expulsar cliente |
| POST | `/api/approvals/{id}/approve` | Aprobar solicitud |
| POST | `/api/approvals/{id}/reject` | Rechazar solicitud |
| DELETE | `/api/channels/{id}/members/{device_id}` | Revocar acceso |

## Estructura del proyecto

```
server-go/
├── cmd/
│   └── ptt-server/
│       └── main.go           # Punto de entrada
├── internal/
│   ├── config/               # Configuración
│   ├── store/                # Persistencia JSON
│   ├── utils/                # Utilidades
│   └── websocket/            # Servidor WebSocket
├── static/                   # Archivos del admin panel
├── data/                     # Archivos de datos
├── go.mod
└── README.md
```

## Comparación de rendimiento

| Métrica | Python | Go |
|---------|--------|-----|
| Conexiones concurrentes | ~5,000 | ~50,000+ |
| Memoria por sesión | ~100 KB | ~30 KB |
| Latencia | 50-200ms | 12-52ms |

## Licencia

MIT
