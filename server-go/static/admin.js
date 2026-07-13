const TOKEN_KEY = "ptt_admin_token";
const REFRESH_MS = 3000;

const loginView = document.getElementById("loginView");
const appView = document.getElementById("appView");
const loginError = document.getElementById("loginError");
const passwordInput = document.getElementById("passwordInput");
const gainSlider = document.getElementById("gainSlider");
const gainValue = document.getElementById("gainValue");
const clientsBody = document.getElementById("clientsBody");
const channelsList = document.getElementById("channelsList");
const blockedList = document.getElementById("blockedList");
const pendingList = document.getElementById("pendingList");
const devicesBody = document.getElementById("devicesBody");
const groupsView = document.getElementById("groupsView");
const pendingSection = document.getElementById("pendingSection");
const liveStatus = document.getElementById("liveStatus");
const statOnline = document.getElementById("statOnline");
const statPending = document.getElementById("statPending");
const statPendingChip = document.getElementById("statPendingChip");
const statTalking = document.getElementById("statTalking");
const statTalkingChip = document.getElementById("statTalkingChip");
const pendingBadge = document.getElementById("pendingBadge");
const toastHost = document.getElementById("toastHost");

let refreshTimer = null;
let lastPendingIds = new Set();
let panelInitialized = false;
let lastRefreshAt = 0;
let refreshInFlight = false;

function token() {
  return localStorage.getItem(TOKEN_KEY) || "";
}

async function api(path, options = {}) {
  const headers = {
    "Content-Type": "application/json",
    "X-Admin-Token": token(),
    ...(options.headers || {}),
  };
  const response = await fetch(path, { ...options, headers });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(data.error || "Error de servidor");
  }
  return data;
}

function showApp(show) {
  loginView.classList.toggle("hidden", show);
  appView.classList.toggle("hidden", !show);
}

function startAutoRefresh() {
  if (refreshTimer) clearInterval(refreshTimer);
  refreshTimer = setInterval(() => {
    refresh({ silent: true }).catch(() => {});
  }, REFRESH_MS);
}

function stopAutoRefresh() {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = null;
  }
}

function showToast(title, body) {
  const toast = document.createElement("div");
  toast.className = "toast";
  toast.innerHTML = `<div class="toast-title">${esc(title)}</div><div class="toast-body">${esc(body)}</div>`;
  toastHost.appendChild(toast);
  setTimeout(() => toast.remove(), 6000);
}

function updateLiveStatus(ok = true) {
  if (!ok) {
    liveStatus.textContent = "Sin conexion con el servidor";
    liveStatus.className = "live-status error";
    return;
  }
  const secs = Math.max(0, Math.round((Date.now() - lastRefreshAt) / 1000));
  liveStatus.textContent = `Actualizacion automatica cada ${REFRESH_MS / 1000}s · hace ${secs}s`;
  liveStatus.className = "live-status live";
}

function clientIsSpeaking(c) {
  if (typeof c.is_speaking === "boolean") return c.is_speaking;
  return !!c.is_transmitting;
}

function updateHeaderStats(data) {
  const clients = data.clients || [];
  const pending = data.config.pending_approvals || [];
  const speakers = clients.filter((c) => clientIsSpeaking(c));

  statOnline.textContent = String(clients.length);
  statPending.textContent = String(pending.length);
  statPendingChip.classList.toggle("active", pending.length > 0);

  if (speakers.length > 0) {
    statTalkingChip.hidden = false;
    statTalking.textContent = String(speakers.length);
    const names = speakers.map((s) => s.username).join(", ");
    statTalkingChip.title = `${names} en ${speakers.map((s) => s.channel || s.pending_channel || "-").join(", ")}`;
  } else {
    statTalkingChip.hidden = true;
    statTalkingChip.title = "";
  }

  if (pending.length > 0) {
    pendingBadge.textContent = `${pending.length} pendiente${pending.length === 1 ? "" : "s"}`;
    pendingBadge.classList.remove("hidden");
  } else {
    pendingBadge.classList.add("hidden");
  }
}

async function login() {
  loginError.textContent = "";
  try {
    const data = await api("/api/login", {
      method: "POST",
      body: JSON.stringify({ password: passwordInput.value }),
    });
    localStorage.setItem(TOKEN_KEY, data.token);
    showApp(true);
    panelInitialized = false;
    lastPendingIds = new Set();
    await refresh({ notify: false });
    panelInitialized = true;
    startAutoRefresh();
  } catch (err) {
    loginError.textContent = err.message;
  }
}

function logout() {
  stopAutoRefresh();
  localStorage.removeItem(TOKEN_KEY);
  lastPendingIds = new Set();
  showApp(false);
}

function esc(text) {
  return String(text ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function accessLabel(access) {
  return access === "approval" ? "Requiere aprobacion" : "Libre";
}

function notifyNewPending(pending, options = {}) {
  const currentIds = new Set(pending.map((p) => p.id));
  const fresh = pending.filter((p) => !lastPendingIds.has(p.id));

  if (options.notify && fresh.length > 0) {
    pendingSection.classList.add("new-pending");
    setTimeout(() => pendingSection.classList.remove("new-pending"), 900);

    fresh.forEach((p) => {
      showToast(
        "Nueva solicitud de acceso",
        `${p.username} quiere entrar a ${p.channel_name}`
      );
    });

    if (document.hidden && "Notification" in window && Notification.permission === "granted") {
      new Notification("PTT - Nueva solicitud", {
        body: `${fresh[0].username} pide acceso a ${fresh[0].channel_name}`,
      });
    }
  }

  lastPendingIds = currentIds;
}

function renderPending(pending) {
  pendingSection.classList.toggle("has-pending", pending.length > 0);

  if (!pending.length) {
    pendingList.innerHTML = `<li class="empty-state">No hay solicitudes pendientes. Cuando alguien pida acceso, aparecera aqui al instante.</li>`;
    return;
  }

  pendingList.innerHTML = pending
    .map(
      (p) => `<li>
        <div class="meta">
          <div class="name">${esc(p.username)} solicita entrar a <strong>${esc(p.channel_name)}</strong></div>
          <div class="sub">IP: ${esc(p.ip)} · MAC/ID: ${esc(p.mac || p.device_id)}</div>
        </div>
        <button class="btn btn-primary small" data-approve-id="${esc(p.id)}">Aprobar</button>
        <button class="btn btn-danger small" data-reject-id="${esc(p.id)}">Rechazar</button>
      </li>`
    )
    .join("");
}

function renderClients(clients) {
  if (!clients.length) {
    clientsBody.innerHTML = `<tr><td colspan="6" class="sub"><div class="empty-state">Nadie conectado en este momento</div></td></tr>`;
    return;
  }

  clientsBody.innerHTML = clients
    .map((c) => {
      const macOrId = c.mac || c.device_id || "-";
      let status;
      if (c.pending_channel) {
        status = `<span class="badge warn">Esperando aprobacion</span>`;
      } else if (clientIsSpeaking(c)) {
        status = `<span class="badge talk">Hablando</span>`;
      } else {
        status = `<span class="badge on">Escuchando</span>`;
      }
      const blockLabel = c.channel || c.pending_channel || "-";
      return `<tr>
        <td>${esc(c.username)}</td>
        <td>${esc(blockLabel)}</td>
        <td>${esc(c.ip)}</td>
        <td><span title="${esc(c.device_id)}">${esc(macOrId)}</span></td>
        <td>${status}</td>
        <td>
          <button class="btn btn-danger small" data-kick="${esc(c.session_id)}">Expulsar</button>
          <button class="btn btn-ghost small" data-block-device="${esc(c.device_id)}">Bloquear ID</button>
        </td>
      </tr>`;
    })
    .join("");
}

function renderChannels(channels) {
  if (!channels.length) {
    channelsList.innerHTML = `<li class="empty-state">No hay bloques configurados</li>`;
    return;
  }

  channelsList.innerHTML = channels
    .map(
      (ch) => `<li>
        <input class="toggle" type="checkbox" data-channel-id="${esc(ch.id)}" ${ch.enabled ? "checked" : ""} />
        <div class="meta">
          <div class="name">${esc(ch.name)}</div>
          <div class="sub">${accessLabel(ch.access || "open")} · ID: ${esc(ch.id)}</div>
        </div>
        <select class="accessSelect" data-access-id="${esc(ch.id)}">
          <option value="open" ${ch.access !== "approval" ? "selected" : ""}>Libre</option>
          <option value="approval" ${ch.access === "approval" ? "selected" : ""}>Con aprobacion</option>
        </select>
        <button class="btn btn-ghost small" data-rename-id="${esc(ch.id)}" data-rename-name="${esc(ch.name)}">Renombrar</button>
        <button class="btn btn-danger small" data-delete-id="${esc(ch.id)}">Eliminar</button>
      </li>`
    )
    .join("");
}

function renderGroups(groups) {
  if (!groups.length) {
    groupsView.innerHTML = `<div class="empty-state">Sin bloques configurados</div>`;
    return;
  }

  groupsView.innerHTML = groups
    .map((g) => {
      const members = g.members.length
        ? g.members
            .map(
              (m) => `<li>
                <div class="meta">
                  <div class="name">${esc(m.username || m.device_id)}</div>
                  <div class="sub">${esc(m.ip_last)} · ${esc(m.mac || m.device_id)}</div>
                </div>
                <button class="btn btn-danger small" data-revoke-channel="${esc(g.channel_id)}" data-revoke-device="${esc(m.device_id)}">Quitar acceso</button>
              </li>`
            )
            .join("")
        : `<li class="sub">${g.access === "open" ? "Bloque libre (sin lista fija)" : "Nadie aprobado aun"}</li>`;
      const online = (g.online || []).length
        ? `<div class="sub online">En linea ahora: ${(g.online || []).map((o) => esc(o.username)).join(", ")}</div>`
        : "";
      return `<div class="groupCard">
        <h3>${esc(g.channel_name)} <span class="badge ${g.access === "approval" ? "warn" : "on"}">${accessLabel(g.access)}</span></h3>
        ${online}
        <ul class="list">${members}</ul>
      </div>`;
    })
    .join("");
}

function renderDevices(devices) {
  if (!devices.length) {
    devicesBody.innerHTML = `<tr><td colspan="6" class="sub"><div class="empty-state">Aun no hay dispositivos registrados</div></td></tr>`;
    return;
  }

  devicesBody.innerHTML = devices
    .map((d) => {
      const custom = d.playback_gain == null ? "" : Number(d.playback_gain).toFixed(1);
      const approved = (d.approved_channel_names || []).join(", ") || "-";
      return `<tr>
        <td>${esc(d.username || "-")}</td>
        <td title="${esc(d.device_id)}">${esc(d.device_id.slice(0, 12))}...</td>
        <td>${esc(d.ip_last)}</td>
        <td>${esc(approved)}</td>
        <td>
          <input class="deviceGain" type="number" min="0.5" max="6" step="0.1" data-device-id="${esc(d.device_id)}" value="${custom}" placeholder="Global (${Number(d.effective_gain).toFixed(1)})" />
        </td>
        <td>
          <button class="btn btn-primary small" data-save-device="${esc(d.device_id)}">Guardar</button>
          <button class="btn btn-ghost small" data-reset-device="${esc(d.device_id)}">Usar global</button>
        </td>
      </tr>`;
    })
    .join("");
}

function renderBlocked(blocked) {
  if (!blocked.length) {
    blockedList.innerHTML = `<li class="empty-state">Sin bloqueos activos</li>`;
    return;
  }

  blockedList.innerHTML = blocked
    .map(
      (b) => `<li>
        <div class="meta">
          <div class="name">${esc(b.type)}: ${esc(b.value)}</div>
          <div class="sub">${esc(b.reason || "Sin motivo")}</div>
        </div>
        <button class="btn btn-danger small" data-unblock-id="${esc(b.id)}">Quitar</button>
      </li>`
    )
    .join("");
}

function isEditingField() {
  const active = document.activeElement;
  if (!active) return false;
  if (active === gainSlider) return true;
  if (active instanceof HTMLInputElement && active.classList.contains("deviceGain")) return true;
  if (active instanceof HTMLInputElement && ["newChannelInput", "blockValue", "blockReason"].includes(active.id)) {
    return true;
  }
  return false;
}

async function refresh(options = { silent: false, notify: false }) {
  if (refreshInFlight) return;
  refreshInFlight = true;

  try {
    const data = await api("/api/status");
    lastRefreshAt = Date.now();
    updateLiveStatus(true);

    const pending = data.config.pending_approvals || [];
    notifyNewPending(pending, { notify: panelInitialized && (options.notify !== false) });
    updateHeaderStats(data);

    if (document.activeElement !== gainSlider) {
      const gain = data.config.playback_gain;
      gainSlider.value = gain;
      gainValue.textContent = `${Number(gain).toFixed(1)}x`;
    }

    if (!isEditingField() || !options.silent) {
      renderPending(pending);
      renderClients(data.clients);
      renderChannels(data.config.channels);
      renderGroups(data.config.groups || []);
      if (!isEditingField()) {
        renderDevices(data.config.devices || []);
      }
      renderBlocked(data.config.blocked);
    } else {
      renderPending(pending);
      renderClients(data.clients);
      updateHeaderStats(data);
    }
  } catch (err) {
    updateLiveStatus(false);
    if (!options.silent) {
      throw err;
    }
  } finally {
    refreshInFlight = false;
  }
}

gainSlider.addEventListener("input", () => {
  gainValue.textContent = `${Number(gainSlider.value).toFixed(1)}x`;
});

document.getElementById("loginBtn").addEventListener("click", login);
passwordInput.addEventListener("keydown", (e) => {
  if (e.key === "Enter") login();
});
document.getElementById("logoutBtn").addEventListener("click", logout);
document.getElementById("refreshBtn").addEventListener("click", () => {
  refresh({ silent: false }).catch((err) => alert(err.message));
});

document.getElementById("saveGainBtn").addEventListener("click", async () => {
  try {
    await api("/api/settings/gain", {
      method: "PUT",
      body: JSON.stringify({ playback_gain: Number(gainSlider.value) }),
    });
    await refresh();
  } catch (err) {
    alert(err.message);
  }
});

document.getElementById("addChannelBtn").addEventListener("click", async () => {
  const name = document.getElementById("newChannelInput").value.trim();
  const access = document.getElementById("newChannelAccess").value;
  if (!name) return;
  try {
    await api("/api/channels", { method: "POST", body: JSON.stringify({ name, access }) });
    document.getElementById("newChannelInput").value = "";
    await refresh();
  } catch (err) {
    alert(err.message);
  }
});

document.getElementById("addBlockBtn").addEventListener("click", async () => {
  const type = document.getElementById("blockType").value;
  const value = document.getElementById("blockValue").value.trim();
  const reason = document.getElementById("blockReason").value.trim();
  if (!value) return;
  try {
    await api("/api/blocked", {
      method: "POST",
      body: JSON.stringify({ type, value, reason }),
    });
    document.getElementById("blockValue").value = "";
    document.getElementById("blockReason").value = "";
    await refresh();
  } catch (err) {
    alert(err.message);
  }
});

pendingList.addEventListener("click", async (e) => {
  const target = e.target;
  if (!(target instanceof HTMLElement)) return;
  const approveId = target.getAttribute("data-approve-id");
  if (approveId) {
    try {
      await api(`/api/approvals/${approveId}/approve`, { method: "POST" });
      await refresh();
    } catch (err) {
      alert(err.message);
    }
    return;
  }
  const rejectId = target.getAttribute("data-reject-id");
  if (rejectId) {
    try {
      await api(`/api/approvals/${rejectId}/reject`, { method: "POST" });
      await refresh();
    } catch (err) {
      alert(err.message);
    }
  }
});

channelsList.addEventListener("click", async (e) => {
  const target = e.target;
  if (!(target instanceof HTMLElement)) return;
  const deleteId = target.getAttribute("data-delete-id");
  if (deleteId) {
    if (!confirm("Eliminar este bloque?")) return;
    try {
      await api(`/api/channels/${deleteId}`, { method: "DELETE" });
      await refresh();
    } catch (err) {
      alert(err.message);
    }
    return;
  }
  const renameId = target.getAttribute("data-rename-id");
  if (renameId) {
    const current = target.getAttribute("data-rename-name") || "";
    const name = prompt("Nuevo nombre:", current);
    if (!name || name.trim() === current) return;
    try {
      await api(`/api/channels/${renameId}`, {
        method: "PUT",
        body: JSON.stringify({ name: name.trim() }),
      });
      await refresh();
    } catch (err) {
      alert(err.message);
    }
  }
});

channelsList.addEventListener("change", async (e) => {
  const target = e.target;
  if (!(target instanceof HTMLInputElement) && !(target instanceof HTMLSelectElement)) return;
  if (target instanceof HTMLInputElement && target.classList.contains("toggle")) {
    const channelId = target.getAttribute("data-channel-id");
    if (!channelId) return;
    try {
      await api(`/api/channels/${channelId}`, {
        method: "PUT",
        body: JSON.stringify({ enabled: target.checked }),
      });
      await refresh();
    } catch (err) {
      alert(err.message);
      target.checked = !target.checked;
    }
    return;
  }
  if (target instanceof HTMLSelectElement && target.classList.contains("accessSelect")) {
    const channelId = target.getAttribute("data-access-id");
    if (!channelId) return;
    try {
      await api(`/api/channels/${channelId}`, {
        method: "PUT",
        body: JSON.stringify({ access: target.value }),
      });
      await refresh();
    } catch (err) {
      alert(err.message);
    }
  }
});

groupsView.addEventListener("click", async (e) => {
  const target = e.target;
  if (!(target instanceof HTMLElement)) return;
  const channelId = target.getAttribute("data-revoke-channel");
  const deviceId = target.getAttribute("data-revoke-device");
  if (!channelId || !deviceId) return;
  if (!confirm("Quitar acceso de este dispositivo al bloque?")) return;
  try {
    await api(`/api/channels/${channelId}/members/${deviceId}`, { method: "DELETE" });
    await refresh();
  } catch (err) {
    alert(err.message);
  }
});

devicesBody.addEventListener("click", async (e) => {
  const target = e.target;
  if (!(target instanceof HTMLElement)) return;
  const deviceId = target.getAttribute("data-save-device") || target.getAttribute("data-reset-device");
  if (!deviceId) return;
  const input = devicesBody.querySelector(`input[data-device-id="${deviceId}"]`);
  const reset = target.hasAttribute("data-reset-device");
  const value = reset || !input || !input.value ? null : Number(input.value);
  try {
    await api(`/api/devices/${deviceId}/gain`, {
      method: "PUT",
      body: JSON.stringify({ playback_gain: value }),
    });
    await refresh();
  } catch (err) {
    alert(err.message);
  }
});

blockedList.addEventListener("click", async (e) => {
  const target = e.target;
  if (!(target instanceof HTMLElement)) return;
  const id = target.getAttribute("data-unblock-id");
  if (!id) return;
  try {
    await api(`/api/blocked/${id}`, { method: "DELETE" });
    await refresh();
  } catch (err) {
    alert(err.message);
  }
});

clientsBody.addEventListener("click", async (e) => {
  const target = e.target;
  if (!(target instanceof HTMLElement)) return;
  const kickId = target.getAttribute("data-kick");
  if (kickId) {
    if (!confirm("Expulsar?")) return;
    try {
      await api(`/api/kick/${kickId}`, { method: "POST" });
      await refresh();
    } catch (err) {
      alert(err.message);
    }
    return;
  }
  const deviceId = target.getAttribute("data-block-device");
  if (deviceId) {
    if (!confirm("Bloquear este dispositivo?")) return;
    try {
      await api("/api/blocked", {
        method: "POST",
        body: JSON.stringify({ type: "device_id", value: deviceId, reason: "Desde panel" }),
      });
      await refresh();
    } catch (err) {
      alert(err.message);
    }
  }
});

if ("Notification" in window && Notification.permission === "default") {
  Notification.requestPermission().catch(() => {});
}

if (token()) {
  showApp(true);
  refresh({ notify: false })
    .catch(() => logout())
    .finally(() => {
      if (token()) {
        panelInitialized = true;
        startAutoRefresh();
      }
    });
}

setInterval(() => {
  if (!appView.classList.contains("hidden") && lastRefreshAt) {
    updateLiveStatus(true);
  }
}, 1000);
