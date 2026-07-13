"""Utilidades del servidor PTT."""

from __future__ import annotations

import re
import subprocess


def lookup_mac(ip: str) -> str:
    """Intenta obtener MAC desde tabla ARP de Windows (misma red local)."""
    if not ip or ip.startswith("127."):
        return ""
    try:
        output = subprocess.check_output(["arp", "-a", ip], text=True, timeout=2, errors="ignore")
    except (subprocess.SubprocessError, OSError):
        return ""
    for line in output.splitlines():
        if ip not in line:
            continue
        match = re.search(r"([0-9a-fA-F]{2}[-:]){5}[0-9a-fA-F]{2}", line)
        if match:
            return match.group(0).replace("-", ":").upper()
    return ""
