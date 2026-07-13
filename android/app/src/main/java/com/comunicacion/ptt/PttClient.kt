package com.comunicacion.ptt

import android.os.Handler
import android.os.HandlerThread
import android.util.Log
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONObject
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger

class PttClient(
    private val listener: Listener
) {
    interface Listener {
        fun onConnected(channels: List<String>)
        fun onJoined(channel: String, users: List<String>)
        fun onServerConfig(playbackGain: Float, channels: List<String>)
        fun onApprovalPending(channel: String, message: String)
        fun onApprovalDenied(message: String)
        fun onUsersUpdated(users: List<String>)
        fun onPttGranted()
        fun onPttDenied(speaker: String)
        fun onPttStarted(username: String)
        fun onPttEnded(username: String)
        fun onAudioReceived(data: ByteArray)
        fun onConnectionLost(message: String?)
        fun onError(message: String)
    }

    private val client = OkHttpClient.Builder()
        .connectTimeout(CONNECT_TIMEOUT_SEC, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.MILLISECONDS)
        .pingInterval(20, TimeUnit.SECONDS)
        .build()

    private val heartbeatThread = HandlerThread("PttHeartbeat").apply { start() }
    private val heartbeatHandler = Handler(heartbeatThread.looper)
    private val connectionGeneration = AtomicInteger(0)

    private var webSocket: WebSocket? = null
    private var socketConnected = false
    private var sessionJoined = false
    private var canTransmit = false
    private var intentionalClose = false
    private var approvalPending = false

    private var lastServerActivityMs = 0L

    private val heartbeatRunnable = object : Runnable {
        override fun run() {
            if ((!sessionJoined && !approvalPending) || intentionalClose) return
            val elapsed = System.currentTimeMillis() - lastServerActivityMs
            if (elapsed > HEARTBEAT_TIMEOUT_MS) {
                Log.w(TAG, "Heartbeat timeout (${elapsed}ms)")
                handleConnectionLost("Perdida de senal con el servidor")
                return
            }
            sendJson(JSONObject().put("type", "ping"))
            heartbeatHandler.postDelayed(this, HEARTBEAT_INTERVAL_MS)
        }
    }

    fun connect(
        serverIp: String,
        channel: String,
        username: String,
        deviceId: String,
        macAddress: String
    ) {
        intentionalClose = false
        sessionJoined = false
        approvalPending = false
        stopHeartbeat()
        val generation = connectionGeneration.incrementAndGet()
        closeSocketSilently()

        val url = "ws://$serverIp:8765"
        val request = Request.Builder().url(url).build()
        webSocket = client.newWebSocket(
            request,
            createListener(generation, channel, username, deviceId, macAddress)
        )
    }

    /** Cierre solicitado por el usuario. No dispara onConnectionLost. */
    fun disconnect() {
        intentionalClose = true
        connectionGeneration.incrementAndGet()
        canTransmit = false
        socketConnected = false
        sessionJoined = false
        approvalPending = false
        stopHeartbeat()
        closeSocketSilently()
    }

    /** Cierre por red caida o heartbeat. Dispara onConnectionLost si no fue intencional. */
    fun closeDueToNetwork(message: String = "Sin conexion WiFi") {
        if (intentionalClose) return
        connectionGeneration.incrementAndGet()
        canTransmit = false
        socketConnected = false
        sessionJoined = false
        stopHeartbeat()
        closeSocketSilently()
        listener.onConnectionLost(message)
    }

    fun startPtt() {
        sendJson(JSONObject().put("type", "ptt_start"))
    }

    fun stopPtt() {
        canTransmit = false
        sendJson(JSONObject().put("type", "ptt_end"))
    }

    fun sendAudio(data: ByteArray) {
        if (canTransmit && socketConnected) {
            webSocket?.send(okio.ByteString.of(*data))
        }
    }

    private fun createListener(
        generation: Int,
        channel: String,
        username: String,
        deviceId: String,
        macAddress: String
    ): WebSocketListener {
        return object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                if (!isCurrentGeneration(generation)) return
                socketConnected = true
                sendJson(
                    JSONObject()
                        .put("type", "join")
                        .put("channel", channel)
                        .put("username", username)
                        .put("device_id", deviceId)
                        .put("mac", macAddress)
                )
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                if (!isCurrentGeneration(generation)) return
                touchServerActivity()
                handleMessage(text)
            }

            override fun onMessage(webSocket: WebSocket, bytes: okio.ByteString) {
                if (!isCurrentGeneration(generation) || canTransmit) return
                touchServerActivity()
                listener.onAudioReceived(bytes.toByteArray())
            }

            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                webSocket.close(code, reason)
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                if (!isCurrentGeneration(generation)) return
                handleConnectionLost(reason.ifBlank { null })
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                if (!isCurrentGeneration(generation)) return
                Log.e(TAG, "WebSocket error", t)
                handleConnectionLost(t.message)
            }
        }
    }

    private fun handleConnectionLost(message: String?) {
        if (intentionalClose) return
        socketConnected = false
        sessionJoined = false
        approvalPending = false
        canTransmit = false
        stopHeartbeat()
        closeSocketSilently()
        listener.onConnectionLost(message)
    }

    private fun touchServerActivity() {
        lastServerActivityMs = System.currentTimeMillis()
    }

    private fun startHeartbeat() {
        stopHeartbeat()
        lastServerActivityMs = System.currentTimeMillis()
        heartbeatHandler.postDelayed(heartbeatRunnable, HEARTBEAT_INTERVAL_MS)
    }

    private fun stopHeartbeat() {
        heartbeatHandler.removeCallbacks(heartbeatRunnable)
    }

    private fun isCurrentGeneration(generation: Int): Boolean {
        return generation == connectionGeneration.get()
    }

    private fun closeSocketSilently() {
        val socket = webSocket
        webSocket = null
        try {
            socket?.cancel()
        } catch (_: Exception) {
        }
    }

    private fun handleMessage(text: String) {
        val json = JSONObject(text)
        when (json.optString("type")) {
            "joined" -> {
                approvalPending = false
                sessionJoined = true
                startHeartbeat()
                val channels = json.getJSONArray("channels").let { arr ->
                    List(arr.length()) { i -> arr.getString(i) }
                }
                val users = json.getJSONArray("users").let { arr ->
                    List(arr.length()) { i -> arr.getString(i) }
                }
                val gain = json.optDouble("playback_gain", AudioConfig.PLAYBACK_GAIN.toDouble()).toFloat()
                listener.onServerConfig(gain, channels)
                listener.onConnected(channels)
                listener.onJoined(json.getString("channel"), users)
            }

            "config_update" -> {
                val channels = json.getJSONArray("channels").let { arr ->
                    List(arr.length()) { i -> arr.getString(i) }
                }
                val gain = json.optDouble("playback_gain", AudioConfig.PLAYBACK_GAIN.toDouble()).toFloat()
                listener.onServerConfig(gain, channels)
            }

            "approval_pending" -> {
                approvalPending = true
                startHeartbeat()
                listener.onApprovalPending(
                    json.optString("channel", ""),
                    json.optString("message", "Esperando aprobacion")
                )
            }

            "approval_denied" -> {
                val message = json.optString("message", "Acceso denegado")
                listener.onApprovalDenied(message)
                handleConnectionLost(message)
            }

            "users_update" -> {
                val users = json.getJSONArray("users").let { arr ->
                    List(arr.length()) { i -> arr.getString(i) }
                }
                listener.onUsersUpdated(users)
            }

            "pong" -> touchServerActivity()

            "ptt_granted" -> {
                canTransmit = true
                listener.onPttGranted()
            }

            "ptt_denied" -> {
                listener.onPttDenied(json.optString("speaker", "Otro usuario"))
            }

            "ptt_started" -> listener.onPttStarted(json.getString("username"))
            "ptt_ended" -> listener.onPttEnded(json.getString("username"))
            "error" -> listener.onError(json.optString("message", "Error desconocido"))
        }
    }

    private fun sendJson(json: JSONObject) {
        webSocket?.send(json.toString())
    }

    companion object {
        private const val TAG = "PttClient"
        private const val CONNECT_TIMEOUT_SEC = 10L
        private const val HEARTBEAT_INTERVAL_MS = 15_000L
        private const val HEARTBEAT_TIMEOUT_MS = 50_000L
    }
}
