"""
Servidor PTT local para comunicacion por WiFi.
WebSocket: puerto 8765 | Panel admin: http://IP:8766/admin
"""

from __future__ import annotations

import sys
from pathlib import Path

_APP_DIR = Path(__file__).resolve().parent
if str(_APP_DIR) not in sys.path:
    sys.path.insert(0, str(_APP_DIR))

import asyncio
import json
import logging
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Dict, List, Optional

import websockets
from aiohttp import web

from admin import create_admin_app
from config import ADMIN_PORT, AUDIO_FORMAT, HOST, PORT
from store import Store
from utils import lookup_mac

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
log = logging.getLogger("ptt-server")


@dataclass
class Client:
    ws: object
    session_id: str
    username: str = "Usuario"
    channel: Optional[str] = None
    pending_channel: Optional[str] = None
    is_transmitting: bool = False
    ip: str = ""
    device_id: str = ""
    mac: str = ""
    connected_at: str = field(default_factory=lambda: datetime.now(timezone.utc).isoformat())


clients: Dict[object, Client] = {}
channel_members: Dict[str, set] = {}
channel_speaker: Dict[str, object] = {}
store = Store()


def is_open(ws: object) -> bool:
    return getattr(ws, "close_code", None) is None


def add_channel_member(channel: str, ws: object) -> None:
    if not channel:
        return
    channel_members.setdefault(channel, set()).add(ws)


def remove_channel_member(channel: str, ws: object) -> None:
    if not channel:
        return
    members = channel_members.get(channel)
    if not members:
        return
    members.discard(ws)
    if not members:
        channel_members.pop(channel, None)


def channel_recipients(channel: str, *, exclude: Optional[object] = None, listeners_only: bool = False) -> List[object]:
    """WebSockets activos en un canal (opcionalmente solo oyentes)."""
    result: List[object] = []
    for ws in channel_members.get(channel, ()):
        if ws == exclude:
            continue
        client = clients.get(ws)
        if not client or not is_open(ws):
            continue
        if listeners_only and client.is_transmitting:
            continue
        result.append(ws)
    return result


def client_ip(ws: object) -> str:
    remote = getattr(ws, "remote_address", None)
    if remote and isinstance(remote, tuple) and remote[0]:
        return str(remote[0])
    return ""


def users_in_channel(channel: str) -> List[str]:
    names = []
    for ws in channel_members.get(channel, ()):
        client = clients.get(ws)
        if client and is_open(ws):
            names.append(client.username)
    return sorted(names)


def is_active_speaker(ws: object, client: Client) -> bool:
    """Fuente de verdad: habla solo quien es el speaker activo del canal."""
    if not client.channel:
        return False
    return channel_speaker.get(client.channel) == ws


def reconcile_ptt_state() -> None:
    """Limpia flags PTT obsoletos (p. ej. ptt_end perdido o desconexion brusca)."""
    for channel, ws in list(channel_speaker.items()):
        if ws not in clients or not is_open(ws):
            del channel_speaker[channel]

    for ws, client in clients.items():
        if not is_open(ws):
            continue
        if client.is_transmitting and not is_active_speaker(ws, client):
            client.is_transmitting = False


def active_speakers_snapshot() -> List[dict]:
    reconcile_ptt_state()
    rows = []
    for channel, ws in channel_speaker.items():
        client = clients.get(ws)
        if not client or not is_open(ws):
            continue
        rows.append(
            {
                "username": client.username,
                "channel": channel,
                "session_id": client.session_id,
            }
        )
    rows.sort(key=lambda row: (row["channel"], row["username"]))
    return rows


def clients_snapshot() -> List[dict]:
    reconcile_ptt_state()
    rows = []
    for ws, client in clients.items():
        if not is_open(ws):
            continue
        speaking = is_active_speaker(ws, client)
        rows.append(
            {
                "session_id": client.session_id,
                "username": client.username,
                "channel": client.channel or "",
                "pending_channel": client.pending_channel or "",
                "ip": client.ip,
                "mac": client.mac,
                "device_id": client.device_id,
                "is_transmitting": client.is_transmitting,
                "is_speaking": speaking,
                "connected_at": client.connected_at,
            }
        )
    rows.sort(key=lambda row: (row["channel"] or row["pending_channel"], row["username"]))
    return rows


def find_ws_by_session(session_id: str) -> Optional[object]:
    for ws, client in clients.items():
        if client.session_id == session_id and is_open(ws):
            return ws
    return None


async def complete_join(ws: object, channel: str) -> None:
    client = clients.get(ws)
    if not client:
        return

    if client.channel and client.channel != channel:
        old_channel = client.channel
        remove_channel_member(old_channel, ws)
        if channel_speaker.get(old_channel) == ws:
            del channel_speaker[old_channel]
            await broadcast_json(old_channel, {"type": "ptt_ended", "username": client.username})
        await notify_users(old_channel)

    client.channel = channel
    client.pending_channel = None
    client.is_transmitting = False
    add_channel_member(channel, ws)
    enabled = store.enabled_channel_names()
    gain = store.device_playback_gain(client.device_id)

    await store.record_device_channel_access(client.device_id, channel)

    await send_json(
        ws,
        {
            "type": "joined",
            "channel": channel,
            "channels": enabled,
            "users": users_in_channel(channel),
            "playback_gain": gain,
            "audio_format": AUDIO_FORMAT,
        },
    )
    await notify_users(channel)
    log.info(
        "%s (%s / %s) entro a %s",
        client.username,
        client.ip,
        client.mac or client.device_id,
        channel,
    )


async def push_device_gain(device_id: str) -> None:
    if not device_id:
        return
    gain = store.device_playback_gain(device_id)
    payload = {
        "type": "config_update",
        "playback_gain": gain,
        "channels": store.enabled_channel_names(),
        "audio_format": AUDIO_FORMAT,
    }
    message = json.dumps(payload, ensure_ascii=False)
    for client in clients.values():
        if client.device_id == device_id and is_open(client.ws):
            try:
                await client.ws.send(message)
            except websockets.ConnectionClosed:
                pass


async def kick_client_async(session_id: str) -> bool:
    for ws, client in list(clients.items()):
        if client.session_id == session_id and is_open(ws):
            await ws.close(code=4000, reason="Expulsado por administrador")
            return True
    return False


async def send_json(ws: object, payload: dict) -> None:
    await ws.send(json.dumps(payload, ensure_ascii=False))


async def broadcast_json(channel: str, payload: dict, exclude: Optional[object] = None) -> None:
    message = json.dumps(payload, ensure_ascii=False)
    targets = channel_recipients(channel, exclude=exclude)
    if not targets:
        return

    async def _safe_send(ws: object) -> None:
        try:
            await ws.send(message)
        except websockets.ConnectionClosed:
            pass

    for ws in targets:
        asyncio.create_task(_safe_send(ws))


async def broadcast_audio(channel: str, sender: object, audio: bytes) -> None:
    """Reenvio de audio sin esperar al oyente mas lento (evita bloquear al hablante)."""
    targets = channel_recipients(channel, exclude=sender, listeners_only=True)
    if not targets:
        return

    async def _safe_send(ws: object) -> None:
        try:
            await ws.send(audio)
        except websockets.ConnectionClosed:
            pass

    for ws in targets:
        asyncio.create_task(_safe_send(ws))


async def broadcast_config_update() -> None:
    payload = {
        "type": "config_update",
        "playback_gain": store.playback_gain(),
        "channels": store.enabled_channel_names(),
        "audio_format": AUDIO_FORMAT,
    }
    message = json.dumps(payload, ensure_ascii=False)
    for client in clients.values():
        if is_open(client.ws):
            try:
                await client.ws.send(message)
            except websockets.ConnectionClosed:
                pass


async def notify_users(channel: str) -> None:
    await broadcast_json(channel, {"type": "users_update", "users": users_in_channel(channel)})


async def handle_join(ws: object, data: dict) -> None:
    client = clients.get(ws)
    if not client:
        return

    channel = data.get("channel", "").strip()
    username = data.get("username", "").strip() or "Usuario"
    device_id = str(data.get("device_id", "")).strip()
    mac = str(data.get("mac", "")).strip()

    client.username = username
    client.device_id = device_id
    client.mac = mac or lookup_mac(client.ip)
    await store.touch_device(device_id, username=username, ip=client.ip, mac=client.mac)

    if store.is_blocked(username, device_id, client.ip):
        await send_json(ws, {"type": "error", "message": "Acceso bloqueado por el administrador"})
        await ws.close(code=4003, reason="Bloqueado")
        return

    enabled = store.enabled_channel_names()
    if channel not in enabled:
        await send_json(
            ws,
            {
                "type": "error",
                "message": f"Bloque invalido. Disponibles: {', '.join(enabled)}",
            },
        )
        return

    channel_info = store.channel_by_name(channel)
    if channel_info and channel_info.get("access") == "approval":
        if not store.is_device_approved_for_channel(device_id, channel):
            client.channel = None
            client.pending_channel = channel
            pending = await store.upsert_pending(
                device_id=device_id,
                username=username,
                ip=client.ip,
                mac=client.mac,
                channel_id=channel_info["id"],
                channel_name=channel,
                session_id=client.session_id,
            )
            await send_json(
                ws,
                {
                    "type": "approval_pending",
                    "channel": channel,
                    "message": "Esperando aprobacion del administrador para este bloque",
                    "request_id": pending.get("id"),
                },
            )
            log.info(
                "Solicitud de acceso: %s -> %s (%s)",
                username,
                channel,
                device_id or client.ip,
            )
            return

    await complete_join(ws, channel)


async def handle_ptt_start(ws: object) -> None:
    client = clients.get(ws)
    if not client or not client.channel:
        await send_json(ws, {"type": "error", "message": "Debe unirse a un bloque primero"})
        return

    if store.is_blocked(client.username, client.device_id, client.ip):
        await send_json(ws, {"type": "error", "message": "Acceso bloqueado por el administrador"})
        return

    channel = client.channel
    current_speaker = channel_speaker.get(channel)
    if current_speaker and (current_speaker not in clients or not is_open(current_speaker)):
        del channel_speaker[channel]
        current_speaker = None

    if current_speaker and current_speaker != ws and current_speaker in clients:
        speaker_name = clients[current_speaker].username
        await send_json(
            ws,
            {"type": "ptt_denied", "reason": "Canal ocupado", "speaker": speaker_name},
        )
        return

    channel_speaker[channel] = ws
    client.is_transmitting = True
    await send_json(ws, {"type": "ptt_granted"})
    await broadcast_json(
        channel,
        {"type": "ptt_started", "username": client.username},
        exclude=ws,
    )
    log.info("%s transmite en %s", client.username, channel)


async def handle_ptt_end(ws: object) -> None:
    client = clients.get(ws)
    if not client or not client.channel:
        return

    channel = client.channel
    if channel_speaker.get(channel) == ws:
        del channel_speaker[channel]

    if client.is_transmitting:
        client.is_transmitting = False
        await broadcast_json(channel, {"type": "ptt_ended", "username": client.username})


async def handle_json(ws: object, data: dict) -> None:
    msg_type = data.get("type")

    if msg_type == "join":
        await handle_join(ws, data)
    elif msg_type == "ptt_start":
        await handle_ptt_start(ws)
    elif msg_type == "ptt_end":
        await handle_ptt_end(ws)
    elif msg_type == "ping":
        await send_json(ws, {"type": "pong"})
    else:
        await send_json(ws, {"type": "error", "message": f"Tipo desconocido: {msg_type}"})


async def cleanup_client(ws: object) -> None:
    client = clients.pop(ws, None)
    if not client:
        return

    await store.remove_pending_by_session(client.session_id)

    if client.pending_channel and not client.channel:
        log.info("%s desconectado (pendiente de aprobacion)", client.username)
        return

    if not client.channel:
        return

    channel = client.channel
    remove_channel_member(channel, ws)
    if channel_speaker.get(channel) == ws:
        del channel_speaker[channel]
        await broadcast_json(channel, {"type": "ptt_ended", "username": client.username})

    await notify_users(channel)
    log.info("%s desconectado de %s", client.username, channel)


async def handler(ws: object) -> None:
    ip = client_ip(ws)
    clients[ws] = Client(ws=ws, session_id=str(uuid.uuid4())[:8], ip=ip, mac=lookup_mac(ip))
    log.info("Cliente conectado: %s (%s)", ip, clients[ws].session_id)

    try:
        async for message in ws:
            if isinstance(message, bytes):
                client = clients.get(ws)
                if (
                    client
                    and client.channel
                    and client.is_transmitting
                    and channel_speaker.get(client.channel) == ws
                ):
                    await broadcast_audio(client.channel, ws, message)
            else:
                await handle_json(ws, json.loads(message))
    except websockets.ConnectionClosed:
        pass
    finally:
        await cleanup_client(ws)


async def on_gain_updated(_request: web.Request) -> web.Response:
    await broadcast_config_update()
    return web.Response(status=204)


async def approve_pending_request(pending_id: str) -> bool:
    item = await store.approve_pending(pending_id)
    if not item:
        return False
    ws = find_ws_by_session(str(item.get("session_id", "")))
    if ws and item.get("channel_name"):
        await complete_join(ws, item["channel_name"])
    return True


async def reject_pending_request(pending_id: str) -> bool:
    item = await store.remove_pending(pending_id)
    if not item:
        return False
    ws = find_ws_by_session(str(item.get("session_id", "")))
    if ws:
        client = clients.get(ws)
        if client:
            client.pending_channel = None
        await send_json(
            ws,
            {
                "type": "approval_denied",
                "channel": item.get("channel_name", ""),
                "message": "Acceso denegado por el administrador",
            },
        )
    return True


async def main() -> None:
    await store.load()
    enabled = store.enabled_channel_names()
    log.info("Iniciando servidor PTT en %s:%s", HOST, PORT)
    log.info("Panel admin: http://<IP-DE-ESTA-PC>:%s/admin", ADMIN_PORT)
    log.info("Clave admin por defecto: %s (cambiar en data/config.json)", store.config.get("admin_password"))
    log.info("Bloques activos: %s", ", ".join(enabled))

    admin_app = create_admin_app(
        store,
        clients_snapshot,
        kick_client_async,
        approve_pending_request,
        reject_pending_request,
        push_device_gain,
    )

    @web.middleware
    async def notify_clients_middleware(request, handler):
        response = await handler(request)
        if response.status < 400 and request.method in {"PUT", "POST", "DELETE"}:
            path = request.path
            if "settings/gain" in path or path.startswith("/api/channels"):
                await broadcast_config_update()
        return response

    admin_app.middlewares.append(notify_clients_middleware)

    runner = web.AppRunner(admin_app)
    await runner.setup()
    admin_site = web.TCPSite(runner, HOST, ADMIN_PORT)
    await admin_site.start()

    async with websockets.serve(handler, HOST, PORT, max_size=2**20, ping_interval=20, ping_timeout=60):
        try:
            await asyncio.Future()
        finally:
            await store.flush()


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        log.info("Servidor detenido")
