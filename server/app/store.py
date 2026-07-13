"""Persistencia de configuracion del panel de administracion."""

from __future__ import annotations

import asyncio
import json
import uuid
from copy import deepcopy
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, List, Optional

from config import SAVE_DEBOUNCE_SECONDS

DATA_DIR = Path(__file__).resolve().parent.parent / "data"
CONFIG_PATH = DATA_DIR / "config.json"

DEFAULT_CONFIG: Dict[str, Any] = {
    "admin_password": "admin123",
    "playback_gain": 3.0,
    "channels": [
        {"id": "canal-1", "name": "Canal 1", "enabled": True, "access": "open"},
        {"id": "canal-2", "name": "Canal 2", "enabled": True, "access": "open"},
        {"id": "canal-3", "name": "Canal 3", "enabled": True, "access": "open"},
        {"id": "canal-4", "name": "Canal 4", "enabled": True, "access": "open"},
        {"id": "canal-5", "name": "Canal 5", "enabled": True, "access": "open"},
        {"id": "mantenimiento", "name": "Mantenimiento", "enabled": True, "access": "approval"},
        {"id": "trazabilidad", "name": "Trazabilidad", "enabled": True, "access": "approval"},
        {"id": "produccion", "name": "Produccion", "enabled": True, "access": "approval"},
        {"id": "calidad", "name": "Calidad", "enabled": True, "access": "approval"},
        {"id": "logistica", "name": "Logistica", "enabled": True, "access": "approval"},
    ],
    "blocked": [],
    "devices": {},
    "pending_approvals": [],
}


def _now_iso() -> str:
    return datetime.now(timezone.utc).isoformat()


def _clamp_gain(gain: float) -> float:
    return max(0.5, min(6.0, float(gain)))


class Store:
    def __init__(self) -> None:
        self._lock = asyncio.Lock()
        self.config: Dict[str, Any] = deepcopy(DEFAULT_CONFIG)
        self._dirty = False
        self._save_task: Optional[asyncio.Task] = None

    async def load(self) -> None:
        DATA_DIR.mkdir(parents=True, exist_ok=True)
        if not CONFIG_PATH.exists():
            await self.save(immediate=True)
            return
        async with self._lock:
            with CONFIG_PATH.open("r", encoding="utf-8") as fh:
                loaded = json.load(fh)
            self.config = self._merge_defaults(loaded)

    async def save(self, *, immediate: bool = False) -> None:
        """Persist config. Por defecto agrupa escrituras para reducir I/O."""
        if immediate:
            await self._cancel_debounced_save()
            await self._write_disk()
            return

        self._dirty = True
        await self._schedule_debounced_save()

    async def flush(self) -> None:
        """Escribe config pendiente de inmediato (apagado del servidor)."""
        await self._cancel_debounced_save()
        await self._write_disk()

    async def _cancel_debounced_save(self) -> None:
        if self._save_task and not self._save_task.done():
            self._save_task.cancel()
            try:
                await self._save_task
            except asyncio.CancelledError:
                pass
        self._save_task = None

    async def _schedule_debounced_save(self) -> None:
        await self._cancel_debounced_save()

        async def _delayed() -> None:
            try:
                await asyncio.sleep(SAVE_DEBOUNCE_SECONDS)
                if self._dirty:
                    await self._write_disk()
            except asyncio.CancelledError:
                pass

        self._save_task = asyncio.create_task(_delayed())

    async def _write_disk(self) -> None:
        async with self._lock:
            DATA_DIR.mkdir(parents=True, exist_ok=True)
            tmp = CONFIG_PATH.with_suffix(".tmp")
            with tmp.open("w", encoding="utf-8") as fh:
                json.dump(self.config, fh, ensure_ascii=False, indent=2)
            tmp.replace(CONFIG_PATH)
            self._dirty = False

    def _merge_defaults(self, loaded: Dict[str, Any]) -> Dict[str, Any]:
        merged = deepcopy(DEFAULT_CONFIG)
        for key in ("admin_password", "playback_gain", "channels", "blocked", "devices", "pending_approvals"):
            if key in loaded:
                merged[key] = loaded[key]
        for ch in merged.get("channels", []):
            if "access" not in ch:
                ch["access"] = "open"
        return merged

    def channel_by_name(self, name: str) -> Optional[Dict[str, Any]]:
        name_l = name.strip().lower()
        for ch in self.config.get("channels", []):
            if ch.get("name", "").strip().lower() == name_l:
                return ch
        return None

    def channel_by_id(self, channel_id: str) -> Optional[Dict[str, Any]]:
        for ch in self.config.get("channels", []):
            if ch.get("id") == channel_id:
                return ch
        return None

    def enabled_channel_names(self) -> List[str]:
        return [
            ch["name"]
            for ch in self.config.get("channels", [])
            if ch.get("enabled", True) and ch.get("name")
        ]

    def playback_gain(self) -> float:
        try:
            gain = float(self.config.get("playback_gain", 3.0))
        except (TypeError, ValueError):
            gain = 3.0
        return _clamp_gain(gain)

    def device_playback_gain(self, device_id: str) -> float:
        device = self.config.get("devices", {}).get(device_id)
        if not device:
            return self.playback_gain()
        custom = device.get("playback_gain")
        if custom is None:
            return self.playback_gain()
        try:
            return _clamp_gain(float(custom))
        except (TypeError, ValueError):
            return self.playback_gain()

    async def set_playback_gain(self, gain: float) -> float:
        gain = _clamp_gain(gain)
        self.config["playback_gain"] = gain
        await self.save(immediate=True)
        return gain

    async def set_device_playback_gain(self, device_id: str, gain: Optional[float]) -> Dict[str, Any]:
        device_id = device_id.strip()
        if not device_id:
            raise ValueError("ID de dispositivo vacio")
        devices = self.config.setdefault("devices", {})
        entry = devices.setdefault(
            device_id,
            {
                "username": "",
                "mac": "",
                "ip_last": "",
                "playback_gain": None,
                "approved_channels": [],
                "first_seen": _now_iso(),
                "last_seen": _now_iso(),
            },
        )
        if gain is None:
            entry["playback_gain"] = None
        else:
            entry["playback_gain"] = _clamp_gain(gain)
        entry["last_seen"] = _now_iso()
        await self.save(immediate=True)
        return entry

    async def touch_device(
        self,
        device_id: str,
        *,
        username: str = "",
        ip: str = "",
        mac: str = "",
    ) -> Dict[str, Any]:
        if not device_id:
            return {}
        devices = self.config.setdefault("devices", {})
        now = _now_iso()
        if device_id not in devices:
            devices[device_id] = {
                "username": username,
                "mac": mac,
                "ip_last": ip,
                "playback_gain": None,
                "approved_channels": [],
                "first_seen": now,
                "last_seen": now,
            }
        entry = devices[device_id]
        if username:
            entry["username"] = username
        if ip:
            entry["ip_last"] = ip
        if mac:
            entry["mac"] = mac
        entry["last_seen"] = now
        await self.save()
        return entry

    async def record_device_channel_access(self, device_id: str, channel_name: str) -> None:
        ch = self.channel_by_name(channel_name)
        if not ch or not device_id:
            return
        devices = self.config.setdefault("devices", {})
        entry = devices.setdefault(
            device_id,
            {
                "username": "",
                "mac": "",
                "ip_last": "",
                "playback_gain": None,
                "approved_channels": [],
                "first_seen": _now_iso(),
                "last_seen": _now_iso(),
            },
        )
        approved = set(entry.get("approved_channels", []))
        approved.add(ch["id"])
        entry["approved_channels"] = sorted(approved)
        entry["last_seen"] = _now_iso()
        await self.save()

    def is_device_approved_for_channel(self, device_id: str, channel_name: str) -> bool:
        ch = self.channel_by_name(channel_name)
        if not ch:
            return False
        if ch.get("access", "open") != "approval":
            return True
        if not device_id:
            return False
        device = self.config.get("devices", {}).get(device_id, {})
        return ch["id"] in device.get("approved_channels", [])

    async def add_channel(self, name: str, access: str = "open") -> Dict[str, Any]:
        name = name.strip()
        if not name:
            raise ValueError("Nombre vacio")
        if access not in {"open", "approval"}:
            raise ValueError("Acceso invalido")
        for ch in self.config["channels"]:
            if ch["name"].lower() == name.lower():
                raise ValueError("Ya existe un bloque con ese nombre")
        entry = {
            "id": str(uuid.uuid4())[:8],
            "name": name,
            "enabled": True,
            "access": access,
        }
        self.config["channels"].append(entry)
        await self.save(immediate=True)
        return entry

    async def update_channel(
        self,
        channel_id: str,
        *,
        name: str | None = None,
        enabled: bool | None = None,
        access: str | None = None,
    ) -> Dict[str, Any]:
        for ch in self.config["channels"]:
            if ch["id"] != channel_id:
                continue
            if name is not None:
                name = name.strip()
                if not name:
                    raise ValueError("Nombre vacio")
                for other in self.config["channels"]:
                    if other["id"] != channel_id and other["name"].lower() == name.lower():
                        raise ValueError("Ya existe un bloque con ese nombre")
                ch["name"] = name
            if enabled is not None:
                ch["enabled"] = bool(enabled)
            if access is not None:
                if access not in {"open", "approval"}:
                    raise ValueError("Acceso invalido")
                ch["access"] = access
            await self.save(immediate=True)
            return ch
        raise KeyError("Bloque no encontrado")

    async def delete_channel(self, channel_id: str) -> None:
        before = len(self.config["channels"])
        self.config["channels"] = [ch for ch in self.config["channels"] if ch["id"] != channel_id]
        if len(self.config["channels"]) == before:
            raise KeyError("Bloque no encontrado")
        for device in self.config.get("devices", {}).values():
            device["approved_channels"] = [
                cid for cid in device.get("approved_channels", []) if cid != channel_id
            ]
        self.config["pending_approvals"] = [
            p for p in self.config.get("pending_approvals", []) if p.get("channel_id") != channel_id
        ]
        await self.save(immediate=True)

    async def upsert_pending(
        self,
        *,
        device_id: str,
        username: str,
        ip: str,
        mac: str,
        channel_id: str,
        channel_name: str,
        session_id: str,
    ) -> Dict[str, Any]:
        pending = self.config.setdefault("pending_approvals", [])
        for item in pending:
            if item.get("device_id") == device_id and item.get("channel_id") == channel_id:
                item.update(
                    {
                        "username": username,
                        "ip": ip,
                        "mac": mac,
                        "session_id": session_id,
                        "requested_at": _now_iso(),
                    }
                )
                await self.save()
                return item
        entry = {
            "id": str(uuid.uuid4())[:8],
            "device_id": device_id,
            "username": username,
            "ip": ip,
            "mac": mac,
            "channel_id": channel_id,
            "channel_name": channel_name,
            "session_id": session_id,
            "requested_at": _now_iso(),
        }
        pending.append(entry)
        await self.save()
        return entry

    async def remove_pending(self, pending_id: str) -> Optional[Dict[str, Any]]:
        pending = self.config.get("pending_approvals", [])
        for item in pending:
            if item.get("id") == pending_id:
                self.config["pending_approvals"] = [p for p in pending if p.get("id") != pending_id]
                await self.save(immediate=True)
                return item
        return None

    async def remove_pending_by_session(self, session_id: str) -> None:
        if not session_id:
            return
        self.config["pending_approvals"] = [
            p
            for p in self.config.get("pending_approvals", [])
            if p.get("session_id") != session_id
        ]
        await self.save()

    async def approve_pending(self, pending_id: str) -> Optional[Dict[str, Any]]:
        item = await self.remove_pending(pending_id)
        if not item:
            return None
        device_id = item.get("device_id", "")
        channel_id = item.get("channel_id", "")
        if device_id and channel_id:
            devices = self.config.setdefault("devices", {})
            entry = devices.setdefault(
                device_id,
                {
                    "username": item.get("username", ""),
                    "mac": item.get("mac", ""),
                    "ip_last": item.get("ip", ""),
                    "playback_gain": None,
                    "approved_channels": [],
                    "first_seen": _now_iso(),
                    "last_seen": _now_iso(),
                },
            )
            approved = set(entry.get("approved_channels", []))
            approved.add(channel_id)
            entry["approved_channels"] = sorted(approved)
            entry["last_seen"] = _now_iso()
            await self.save(immediate=True)
        return item

    async def revoke_device_from_channel(self, device_id: str, channel_id: str) -> None:
        device = self.config.get("devices", {}).get(device_id)
        if not device:
            raise KeyError("Dispositivo no encontrado")
        device["approved_channels"] = [
            cid for cid in device.get("approved_channels", []) if cid != channel_id
        ]
        await self.save(immediate=True)

    def list_devices(self) -> List[Dict[str, Any]]:
        rows = []
        channel_names = {ch["id"]: ch["name"] for ch in self.config.get("channels", [])}
        for device_id, data in self.config.get("devices", {}).items():
            approved_names = [
                channel_names.get(cid, cid) for cid in data.get("approved_channels", [])
            ]
            custom_gain = data.get("playback_gain")
            rows.append(
                {
                    "device_id": device_id,
                    "username": data.get("username", ""),
                    "mac": data.get("mac", ""),
                    "ip_last": data.get("ip_last", ""),
                    "playback_gain": custom_gain,
                    "effective_gain": self.device_playback_gain(device_id),
                    "approved_channels": data.get("approved_channels", []),
                    "approved_channel_names": approved_names,
                    "first_seen": data.get("first_seen", ""),
                    "last_seen": data.get("last_seen", ""),
                }
            )
        rows.sort(key=lambda row: (row["username"] or row["device_id"]).lower())
        return rows

    def devices_by_channel(self) -> List[Dict[str, Any]]:
        rows = []
        devices = self.config.get("devices", {})
        for ch in self.config.get("channels", []):
            if not ch.get("enabled", True):
                continue
            members = []
            for device_id, data in devices.items():
                if ch["id"] in data.get("approved_channels", []):
                    members.append(
                        {
                            "device_id": device_id,
                            "username": data.get("username", ""),
                            "mac": data.get("mac", ""),
                            "ip_last": data.get("ip_last", ""),
                        }
                    )
            online = []
            members.sort(key=lambda m: (m["username"] or m["device_id"]).lower())
            rows.append(
                {
                    "channel_id": ch["id"],
                    "channel_name": ch["name"],
                    "access": ch.get("access", "open"),
                    "members": members,
                    "member_count": len(members),
                }
            )
        return rows

    def is_blocked(self, username: str, device_id: str, ip: str) -> bool:
        username_l = username.strip().lower()
        device_l = device_id.strip().lower()
        ip = ip.strip()
        for entry in self.config.get("blocked", []):
            kind = entry.get("type", "")
            value = str(entry.get("value", "")).strip().lower()
            if kind == "username" and username_l and username_l == value:
                return True
            if kind == "device_id" and device_l and device_l == value:
                return True
            if kind == "ip" and ip and ip == str(entry.get("value", "")).strip():
                return True
        return False

    async def add_block(self, block_type: str, value: str, reason: str = "") -> Dict[str, Any]:
        value = value.strip()
        if block_type not in {"username", "device_id", "ip"}:
            raise ValueError("Tipo invalido")
        if not value:
            raise ValueError("Valor vacio")
        entry = {
            "id": str(uuid.uuid4())[:8],
            "type": block_type,
            "value": value,
            "reason": reason.strip(),
            "blocked_at": _now_iso(),
        }
        self.config.setdefault("blocked", []).append(entry)
        await self.save(immediate=True)
        return entry

    async def remove_block(self, block_id: str) -> None:
        blocked = self.config.get("blocked", [])
        new_list = [b for b in blocked if b.get("id") != block_id]
        if len(new_list) == len(blocked):
            raise KeyError("Bloqueo no encontrado")
        self.config["blocked"] = new_list
        await self.save(immediate=True)

    def verify_password(self, password: str) -> bool:
        return password == str(self.config.get("admin_password", ""))
