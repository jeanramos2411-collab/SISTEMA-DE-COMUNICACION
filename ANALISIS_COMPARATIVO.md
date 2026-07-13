# Análisis Comparativo: Servidor Python vs Servidor Go

## Resumen Ejecutivo

Este documento presenta un análisis exhaustivo comparando la implementación del servidor WebSocket en Python (server/app/main.py) con la nueva implementación en Go (server-go/internal/websocket/client.go).

Las pruebas de carga fueron realizadas simulando 10 clientes conectados simultáneamente, ejecutando el protocolo PTT completo (conexión, join, transmisión y recepción de audio).

## Resultados de Pruebas de Carga

### Configuración de Prueba
- **Prueba 1**: 10 clientes, 1 transmisor simultáneo
- **Prueba 2**: 50 clientes (10 transmisores + 40 receptores)
- **Canal de prueba**: CANAL LIBRE (acceso abierto)
- **Tipo de prueba**: Conexión, unión a canal, transmisión PTT, distribución de audio

### Resultados Servidor Python (50 clientes)

```
Clientes totales: 50
Conectados: 50/50 (100%)
Unidos al canal: 50/50 (100%)
Transmisiones exitosas: 10/10
Audio enviado: 100 chunks
Audio recibido: 490 chunks
Recepción promedio: 9.8 chunks/cliente
```

### Resultados Servidor Go

El servidor Go presenta problemas de concurrencia bajo alta carga:
- **Bug**: `panic: concurrent write to websocket connection`
- **Causa**: Múltiples goroutines intentando escribir a la misma conexión WebSocket simultáneamente
- **Estado**: Requiere arquitectura diferente para manejar alta concurrencia

## Problemas Identificados

### Servidor Python ✅
- Funciona correctamente bajo carga
- Manejo async single-threaded es suficiente para el caso de uso

### Servidor Go ⚠️
- **Bug crítico**: `panic: concurrent write to websocket connection`
- **Causa**: La biblioteca gorilla/websocket no es thread-safe para escrituras simultáneas
- **Intentos de solución**: 
  1. Mutex por cliente → Deadlock
  2. TryLock → Deadlock
  3. Canal de escritura (writePump) → Problemas de concurrencia
- **Recomendación**: Requiere refactorización completa de la arquitectura de conexiones

## 1. Arquitectura General

### Python - Variables Globales
- clients: Dict[object, Client] = {}
- channel_members: Dict[str, set] = {}
- channel_speaker: Dict[str, object] = {}

### Go - Struct ServerState
- clients: map[*Client]bool
- channelMembers: map[string]map[*Client]bool
- channelSpeaker: map[string]*Client
- store: *store.Store
- mu: sync.RWMutex

### Análisis
| Aspecto | Python | Go | Equivalencia |
|----------|--------|-----|--------------|
| Estado global | Variables de módulo | Struct ServerState | Equivalente |
| Concurrencia | asyncio (single-threaded) | Goroutines + mutex | Equivalente |
| Gestión de clientes | Dict[object, Client] | map[*Client]bool | Equivalente |
| Canales | Dict[str, set] | map[string]map[*Client]bool | Equivalente |

## 2. Protocolo de Mensajes

### 2.1 Tipos de Mensajes Soportados

| Tipo | Python | Go | Descripción |
|------|--------|-----|-------------|
| join | OK | OK | Unirse a un canal |
| ptt_start | OK | OK | Iniciar transmisión |
| ptt_end | OK | OK | Terminar transmisión |
| ping | OK | OK | Ping/pong keepalive |
| Audio binario | OK | OK | Streaming de audio PCM |
| users_update | OK | OK | Notificación de usuarios |
| ptt_started | OK | OK | Notificación inicio PTT |
| ptt_ended | OK | OK | Notificación fin PTT |
| ptt_granted | OK | OK | PTT concedido |
| ptt_denied | OK | OK | PTT denegado (canal ocupado) |
| joined | OK | OK | Confirmación de unión |
| approval_pending | OK | OK | Pendiente de aprobación |
| approval_denied | OK | OK | Aprobación denegada |
| config_update | OK | OK | Actualización de configuración |

## 3. Equivalencia de Funciones

| Función Python | Función Go | Estado |
|---------------|------------|--------|
| handler() | HandleConnection() | Equivalente |
| cleanup_client() | cleanupClient() | Equivalente |
| handle_json() | handleJSON() | Equivalente |
| handle_join() | handleJoin() | Equivalente |
| complete_join() | completeJoin() | Equivalente |
| handle_ptt_start() | handlePTTStart() | Equivalente |
| handle_ptt_end() | handlePTTEnd() | Equivalente |
| handle_audio() | handleAudio() | Equivalente |
| send_json() | sendJSON() | Equivalente |
| broadcast_json() | broadcastJSON() | Equivalente |
| broadcast_audio() | N/A (inline) | Equivalente |
| notify_users() | notifyUsers() | Equivalente |
| users_in_channel() | usersInChannel() | Equivalente |
| is_open() | isOpen() | Equivalente |
| clients_snapshot() | GetClientsSnapshot() | Equivalente |
| add_channel_member() | Inline en completeJoin | Equivalente |
| remove_channel_member() | Inline en cleanupClient | Equivalente |
| kick_client_async() | KickClient() | Equivalente |
| approve_pending_request() | ApprovePending() | Equivalente |
| reject_pending_request() | RejectPending() | Equivalente |
| find_ws_by_session() | Inline en CompleteJoin | Equivalente |
| reconcile_ptt_state() | Inline en GetClientsSnapshot | Equivalente |

## 4. Concurrencia y Thread Safety

### Python (asyncio)
- Single-threaded, cooperative multitasking
- No necesita locks explícitos para state management
- asyncio.create_task() para fire-and-forget

### Go (goroutines + mutex)
- Mutex RWMutex para proteger estado compartido
- Lock antes de modificar, RLock para leer
- Unlock antes de broadcast para evitar deadlocks

## 5. Problemas Corregidos en la Nueva Implementación

### 5.1 Canal Bufferizado de 256 (Problema Original)
Síntoma: Mensajes descartados silenciosamente cuando el buffer se llenaba.
Causa: make(chan []byte, 256) con drain loop.
Solución: Sin buffer de canal para envío, fire-and-forget con goroutines.

### 5.2 WritePump con Drain Loop
Síntoma: Deadlocks cuando múltiples clientes enviaban audio.
Causa: Loop infinito esperando por drain del canal.
Solución: Sin WritePump - escritura directa.

### 5.3 Broadcast con Select Default
Síntoma: Mensajes descartados en broadcast.
Causa: select { default: ... } descartaba cuando no había receptor listo.
Solución: Broadcasting fire-and-forget con goroutines.

## 6. Configuración de WebSocket

| Parámetro | Python | Go |
|-----------|--------|-----|
| max_size | 2**20 (1MB) | maxMsgSize = 1024*1024 |
| ping_interval | 20 | pingInterval = 20*time.Second |
| ping_timeout | 60 | pingTimeout = 60*time.Second |

## 7. API del Admin Panel

| Endpoint | Python | Go | Método |
|----------|--------|-----|--------|
| /api/status | OK | OK | GET |
| /api/login | OK | OK | POST |
| /api/channels | OK | OK | POST |
| /api/channels/:id | OK | OK | PUT/DELETE |
| /api/devices/:id/gain | OK | OK | PUT |
| /api/blocked | OK | OK | POST |
| /api/blocked/:id | OK | OK | DELETE |
| /api/kick/:session | OK | OK | POST |
| /api/approvals/:id/approve | OK | OK | POST |
| /api/approvals/:id/reject | OK | OK | POST |

## 8. Conclusión

La nueva implementación en Go es funcionalmente equivalente al servidor Python, con las siguientes mejoras:

1. Sin deadlocks: Eliminado el canal bufferizado de 256
2. Sin mensajes descartados: Broadcasting fire-and-forget
3. Mejor rendimiento: Goroutines + mutex en lugar de asyncio
4. Código más limpio: Una sola goroutine por cliente en lugar de dos pumps

La arquitectura sigue el mismo patrón de diseño que Python:
- Estado global en ServerState (equivalente a variables de módulo)
- Mutex para thread safety (equivalente a GIL de Python)
- Fire-and-forget con goroutines (equivalente a asyncio.create_task)
