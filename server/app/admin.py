"""Panel web de administracion PTT (HTTP)."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any, Awaitable, Callable

from aiohttp import web

STATIC_DIR = Path(__file__).resolve().parent / "static"


def _json_response(data: Any, status: int = 200) -> web.Response:
    return web.Response(
        text=json.dumps(data, ensure_ascii=False),
        content_type="application/json",
        status=status,
    )


def _auth_failed() -> web.Response:
    return _json_response({"error": "No autorizado"}, status=401)


def create_admin_app(
    store,
    get_clients_snapshot: Callable[[], list],
    kick_client: Callable[[str], Awaitable[bool]],
    approve_pending: Callable[[str], Awaitable[bool]],
    reject_pending: Callable[[str], Awaitable[bool]],
    push_device_gain: Callable[[str], Awaitable[None]],
) -> web.Application:
    app = web.Application()

    def check_auth(request: web.Request) -> bool:
        token = request.headers.get("X-Admin-Token", "")
        return store.verify_password(token)

    async def index(_request: web.Request) -> web.FileResponse:
        return web.FileResponse(STATIC_DIR / "admin.html")

    async def api_login(request: web.Request) -> web.Response:
        body = await request.json()
        password = str(body.get("password", ""))
        if not store.verify_password(password):
            return _json_response({"error": "Clave incorrecta"}, status=403)
        return _json_response({"ok": True, "token": password})

    async def api_status(request: web.Request) -> web.Response:
        if not check_auth(request):
            return _auth_failed()

        online_by_channel: dict[str, list] = {}
        for client in get_clients_snapshot():
            key = client.get("channel") or client.get("pending_channel") or ""
            if not key:
                continue
            online_by_channel.setdefault(key, []).append(client)

        groups = store.devices_by_channel()
        for group in groups:
            group["online"] = online_by_channel.get(group["channel_name"], [])

        return _json_response(
            {
                "clients": get_clients_snapshot(),
                "config": {
                    "playback_gain": store.playback_gain(),
                    "channels": store.config.get("channels", []),
                    "blocked": store.config.get("blocked", []),
                    "devices": store.list_devices(),
                    "pending_approvals": store.config.get("pending_approvals", []),
                    "groups": groups,
                },
            }
        )

    async def api_set_gain(request: web.Request) -> web.Response:
        if not check_auth(request):
            return _auth_failed()
        body = await request.json()
        gain = await store.set_playback_gain(body.get("playback_gain", 3.0))
        return _json_response({"playback_gain": gain})

    async def api_set_device_gain(request: web.Request) -> web.Response:
        if not check_auth(request):
            return _auth_failed()
        device_id = request.match_info["device_id"]
        body = await request.json()
        raw = body.get("playback_gain")
        gain = None if raw is None or raw == "" else float(raw)
        try:
            entry = await store.set_device_playback_gain(device_id, gain)
        except ValueError as exc:
            return _json_response({"error": str(exc)}, status=400)
        await push_device_gain(device_id)
        return _json_response(
            {
                "device_id": device_id,
                "playback_gain": entry.get("playback_gain"),
                "effective_gain": store.device_playback_gain(device_id),
            }
        )

    async def api_add_channel(request: web.Request) -> web.Response:
        if not check_auth(request):
            return _auth_failed()
        body = await request.json()
        try:
            entry = await store.add_channel(
                str(body.get("name", "")),
                str(body.get("access", "open")),
            )
        except ValueError as exc:
            return _json_response({"error": str(exc)}, status=400)
        return _json_response({"channel": entry})

    async def api_update_channel(request: web.Request) -> web.Response:
        if not check_auth(request):
            return _auth_failed()
        channel_id = request.match_info["channel_id"]
        body = await request.json()
        try:
            entry = await store.update_channel(
                channel_id,
                name=body.get("name"),
                enabled=body.get("enabled"),
                access=body.get("access"),
            )
        except KeyError:
            return _json_response({"error": "Bloque no encontrado"}, status=404)
        except ValueError as exc:
            return _json_response({"error": str(exc)}, status=400)
        return _json_response({"channel": entry})

    async def api_delete_channel(request: web.Request) -> web.Response:
        if not check_auth(request):
            return _auth_failed()
        channel_id = request.match_info["channel_id"]
        try:
            await store.delete_channel(channel_id)
        except KeyError:
            return _json_response({"error": "Bloque no encontrado"}, status=404)
        return _json_response({"ok": True})

    async def api_add_block(request: web.Request) -> web.Response:
        if not check_auth(request):
            return _auth_failed()
        body = await request.json()
        try:
            entry = await store.add_block(
                str(body.get("type", "")),
                str(body.get("value", "")),
                str(body.get("reason", "")),
            )
        except ValueError as exc:
            return _json_response({"error": str(exc)}, status=400)
        return _json_response({"blocked": entry})

    async def api_remove_block(request: web.Request) -> web.Response:
        if not check_auth(request):
            return _auth_failed()
        block_id = request.match_info["block_id"]
        try:
            await store.remove_block(block_id)
        except KeyError:
            return _json_response({"error": "Bloqueo no encontrado"}, status=404)
        return _json_response({"ok": True})

    async def api_kick(request: web.Request) -> web.Response:
        if not check_auth(request):
            return _auth_failed()
        session_id = request.match_info["session_id"]
        if not await kick_client(session_id):
            return _json_response({"error": "Usuario no encontrado"}, status=404)
        return _json_response({"ok": True})

    async def api_approve(request: web.Request) -> web.Response:
        if not check_auth(request):
            return _auth_failed()
        pending_id = request.match_info["pending_id"]
        if not await approve_pending(pending_id):
            return _json_response({"error": "Solicitud no encontrada"}, status=404)
        return _json_response({"ok": True})

    async def api_reject(request: web.Request) -> web.Response:
        if not check_auth(request):
            return _auth_failed()
        pending_id = request.match_info["pending_id"]
        if not await reject_pending(pending_id):
            return _json_response({"error": "Solicitud no encontrada"}, status=404)
        return _json_response({"ok": True})

    async def api_revoke_member(request: web.Request) -> web.Response:
        if not check_auth(request):
            return _auth_failed()
        channel_id = request.match_info["channel_id"]
        device_id = request.match_info["device_id"]
        try:
            await store.revoke_device_from_channel(device_id, channel_id)
        except KeyError as exc:
            return _json_response({"error": str(exc)}, status=404)
        return _json_response({"ok": True})

    async def api_public_info(_request: web.Request) -> web.Response:
        """Info publica para la app Android (sin clave). Solo red local."""
        from config import AUDIO_FORMAT

        return _json_response(
            {
                "ok": True,
                "service": "ptt-comunicacion",
                "channels": store.enabled_channel_names(),
                "audio_format": AUDIO_FORMAT,
            }
        )

    app.router.add_get("/", index)
    app.router.add_get("/admin", index)
    app.router.add_static("/static/", STATIC_DIR, show_index=False)
    app.router.add_post("/api/login", api_login)
    app.router.add_get("/api/public/info", api_public_info)
    app.router.add_get("/api/status", api_status)
    app.router.add_put("/api/settings/gain", api_set_gain)
    app.router.add_put("/api/devices/{device_id}/gain", api_set_device_gain)
    app.router.add_post("/api/channels", api_add_channel)
    app.router.add_put("/api/channels/{channel_id}", api_update_channel)
    app.router.add_delete("/api/channels/{channel_id}", api_delete_channel)
    app.router.add_post("/api/blocked", api_add_block)
    app.router.add_delete("/api/blocked/{block_id}", api_remove_block)
    app.router.add_post("/api/kick/{session_id}", api_kick)
    app.router.add_post("/api/approvals/{pending_id}/approve", api_approve)
    app.router.add_post("/api/approvals/{pending_id}/reject", api_reject)
    app.router.add_delete("/api/channels/{channel_id}/members/{device_id}", api_revoke_member)

    return app
