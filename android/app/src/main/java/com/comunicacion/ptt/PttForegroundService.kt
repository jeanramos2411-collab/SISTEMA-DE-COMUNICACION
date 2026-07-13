package com.comunicacion.ptt

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Binder
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.os.PowerManager
import android.util.Log
import android.view.View
import android.widget.RemoteViews
import androidx.core.app.NotificationCompat
import androidx.core.app.ServiceCompat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import java.util.concurrent.CopyOnWriteArrayList

class PttForegroundService : Service(), PttClient.Listener, NetworkMonitor.Callback {

    enum class SessionPhase {
        IDLE,
        CONNECTING,
        WAITING_APPROVAL,
        CONNECTED,
        RECONNECTING,
    }

    private val binder = LocalBinder()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private val mainHandler = Handler(Looper.getMainLooper())
    private val uiListeners = CopyOnWriteArrayList<UiListener>()

    private lateinit var pttClient: PttClient
    private lateinit var audioEngine: AudioEngine
    private lateinit var networkMonitor: NetworkMonitor
    private var volumeKeyController: PttVolumeKeyController? = null

    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null
    private var sessionPhase = SessionPhase.IDLE
    private var userWantsSession = false
    private var isTransmitting = false
    private var pttRequested = false
    private var reconnectAttempt = 0
    private var wasEverConnected = false

    private var serverIp = ""
    private var currentChannel: String? = null
    private var currentUsers: List<String> = emptyList()
    private var currentSpeaker: String? = null
    private var currentUsername: String = ""
    private var deviceId: String = ""
    private var deviceMac: String = ""
    private var lastRemoteOpeningAtMs = 0L

    interface UiListener {
        fun onConnected(channels: List<String>)
        fun onJoined(channel: String, users: List<String>)
        fun onServerConfig(playbackGain: Float, channels: List<String>)
        fun onApprovalPending(channel: String, message: String)
        fun onApprovalDenied(message: String)
        fun onUsersUpdated(users: List<String>)
        fun onConnecting()
        fun onReconnecting(attempt: Int)
        fun onPttGranted()
        fun onPttDenied(speaker: String)
        fun onPttStarted(username: String)
        fun onPttEnded(username: String)
        fun onSessionEnded(message: String?)
        fun onError(message: String)
    }

    inner class LocalBinder : Binder() {
        fun getService(): PttForegroundService = this@PttForegroundService
    }

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
        pttClient = PttClient(this)
        
        val prefs = Prefs(applicationContext)
        audioEngine = AudioEngine(applicationContext, scope, { frame ->
            pttClient.sendAudio(frame)
        }, prefs.autoVolumeControl)
        
        networkMonitor = NetworkMonitor(this, this)
        networkMonitor.start()
        volumeKeyController = PttVolumeKeyController(
            this,
            onPttStart = { if (sessionPhase == SessionPhase.CONNECTED) startPtt() },
            onPttStop = { stopPtt() }
        )
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_CONNECT -> {
                val ip = intent.getStringExtra(EXTRA_SERVER_IP).orEmpty()
                val channel = intent.getStringExtra(EXTRA_CHANNEL).orEmpty()
                val username = intent.getStringExtra(EXTRA_USERNAME).orEmpty()
                if (ip.isNotBlank() && channel.isNotBlank() && username.isNotBlank()) {
                    beginSession(ip, channel, username)
                } else {
                    endSession(null)
                }
            }

            ACTION_DISCONNECT -> endSession(null)
            ACTION_PTT_TOGGLE -> togglePttFromNotification()
        }
        return START_NOT_STICKY
    }

    override fun onBind(intent: Intent?): IBinder = binder

    override fun onTaskRemoved(rootIntent: Intent?) {
        // Este método se llama cuando el usuario cierra la app (swipe away)
        // Incluso con stopWithTask="false", esto nos permite detener el servicio
        forceStopService()
        super.onTaskRemoved(rootIntent)
    }

    fun addUiListener(listener: UiListener) {
        uiListeners.add(listener)
        syncStateTo(listener)
    }

    fun removeUiListener(listener: UiListener) {
        uiListeners.remove(listener)
    }

    fun getSessionPhase(): SessionPhase = sessionPhase

    fun getSessionState(): SessionSnapshot = SessionSnapshot(
        phase = sessionPhase,
        channel = currentChannel,
        users = currentUsers,
        speaker = currentSpeaker,
        transmitting = isTransmitting,
    )

    /** Fuerza la detención completa del servicio, usado al cerrar la app completamente. */
    fun forceStopService() {
        userWantsSession = false
        cancelReconnect()
        cancelConnectTimeout()
        pttRequested = false

        if (isTransmitting) {
            stopTransmittingInternal()
        }
        pttClient.disconnect()
        audioEngine.release()
        releaseWakeLock()
        releaseWifiLock()

        sessionPhase = SessionPhase.IDLE
        currentChannel = null
        currentUsers = emptyList()
        currentSpeaker = null
        serverIp = ""
        lastRemoteOpeningAtMs = 0L

        deactivateVolumePtt()
        stopSelfSafely()
    }

    fun beginSession(serverIp: String, channel: String, username: String) {
        cancelReconnect()
        userWantsSession = true
        reconnectAttempt = 0
        wasEverConnected = false
        this.serverIp = serverIp
        currentChannel = channel
        currentUsername = username
        isTransmitting = false
        pttRequested = false
        currentSpeaker = null
        currentUsers = emptyList()
        lastRemoteOpeningAtMs = 0L
        deviceId = DeviceInfo.getDeviceId(this)
        deviceMac = DeviceInfo.getMacAddress(this)

        sessionPhase = SessionPhase.CONNECTING
        promoteToForeground(getString(R.string.status_connecting), channel)
        acquireWakeLock()
        acquireWifiLock()
        audioEngine.startPlayback()
        notifyUi { it.onConnecting() }
        pttClient.connect(serverIp, channel, username, deviceId, deviceMac)
        scheduleConnectTimeout()
    }

    fun endSession(message: String?) {
        userWantsSession = false
        cancelReconnect()
        cancelConnectTimeout()
        pttRequested = false

        if (isTransmitting) {
            stopTransmittingInternal()
        }
        pttClient.disconnect()
        audioEngine.release()
        releaseWakeLock()
        releaseWifiLock()

        sessionPhase = SessionPhase.IDLE
        currentChannel = null
        currentUsers = emptyList()
        currentSpeaker = null
        serverIp = ""
        lastRemoteOpeningAtMs = 0L

        deactivateVolumePtt()
        stopSelfSafely()
        notifyUi { it.onSessionEnded(message) }
    }

    fun startPtt() {
        if (sessionPhase != SessionPhase.CONNECTED) return
        if (isTransmitting || pttRequested) return

        val otherSpeaker = currentSpeaker
        if (!otherSpeaker.isNullOrBlank() && otherSpeaker != currentUsername) {
            audioEngine.playPttTone(AudioEngine.PttTone.BUSY)
            notifyUi { it.onPttDenied(otherSpeaker) }
            return
        }

        pttRequested = true
        pttClient.startPtt()
        updateNotification()
    }

    fun stopPtt() {
        pttRequested = false
        if (isTransmitting) {
            stopTransmittingInternal()
        } else {
            pttClient.stopPtt()
            updateNotification()
        }
    }

    fun togglePttFromNotification() {
        if (sessionPhase != SessionPhase.CONNECTED) return
        if (isTransmitting || pttRequested) {
            stopPtt()
        } else {
            startPtt()
        }
    }

    private fun activateVolumePtt() {
        Log.i(TAG, "Activando PTT por volumen - usuario conectado al canal")
        volumeKeyController?.activate()
    }

    private fun deactivateVolumePtt() {
        Log.i(TAG, "Desactivando PTT por volumen")
        volumeKeyController?.deactivate()
    }

    override fun onNetworkLost() {
        if (!userWantsSession) return
        if (sessionPhase != SessionPhase.CONNECTED && sessionPhase != SessionPhase.CONNECTING) return
        Log.i(TAG, "Red perdida")
        pttClient.closeDueToNetwork()
    }

    override fun onNetworkAvailable() {
        if (!userWantsSession) return
        if (sessionPhase != SessionPhase.RECONNECTING) return

        Log.i(TAG, "Red recuperada, reconectando...")
        reconnectAttempt = 0
        attemptReconnectNow()
    }

    override fun onConnectionLost(message: String?) {
        if (!userWantsSession) return

        cancelConnectTimeout()
        isTransmitting = false
        pttRequested = false

        if (!wasEverConnected) {
            if (!networkMonitor.isOnline()) {
                sessionPhase = SessionPhase.RECONNECTING
                updateNotification(getString(R.string.status_no_network))
                notifyUi { it.onReconnecting(1) }
                return
            }
            endSession(message)
            return
        }

        startReconnecting(message)
    }

    private fun startReconnecting(@Suppress("UNUSED_PARAMETER") message: String?) {
        deactivateVolumePtt()
        sessionPhase = SessionPhase.RECONNECTING
        updateNotification(getString(R.string.status_reconnecting))
        notifyUi { it.onReconnecting(reconnectAttempt + 1) }
        scheduleReconnect()
    }

    private fun scheduleReconnect() {
        cancelReconnect()
        if (!userWantsSession) return

        if (reconnectAttempt >= MAX_RECONNECT_ATTEMPTS) {
            endSession(getString(R.string.error_reconnect_failed))
            return
        }

        val delay = RECONNECT_DELAYS_MS[reconnectAttempt.coerceAtMost(RECONNECT_DELAYS_MS.lastIndex)]
        reconnectAttempt++

        reconnectRunnable = Runnable {
            if (!userWantsSession) return@Runnable
            if (!networkMonitor.isOnline()) {
                updateNotification(getString(R.string.status_no_network))
                notifyUi { it.onReconnecting(reconnectAttempt) }
                scheduleReconnect()
                return@Runnable
            }
            attemptReconnectNow()
        }
        mainHandler.postDelayed(reconnectRunnable!!, delay)
    }

    private var reconnectRunnable: Runnable? = null

    private fun attemptReconnectNow() {
        if (!userWantsSession || serverIp.isBlank() || currentChannel.isNullOrBlank()) return

        sessionPhase = SessionPhase.CONNECTING
        updateNotification(getString(R.string.status_reconnecting))
        notifyUi { it.onReconnecting(reconnectAttempt) }
        pttClient.connect(serverIp, currentChannel!!, currentUsername, deviceId, deviceMac)
        scheduleConnectTimeout()
    }

    private fun cancelReconnect() {
        reconnectRunnable?.let { mainHandler.removeCallbacks(it) }
        reconnectRunnable = null
    }

    private var connectTimeoutRunnable: Runnable? = null

    private fun scheduleConnectTimeout() {
        cancelConnectTimeout()
        connectTimeoutRunnable = Runnable {
            if (sessionPhase == SessionPhase.CONNECTING && userWantsSession) {
                Log.w(TAG, "Timeout de conexion")
                pttClient.closeDueToNetwork(getString(R.string.error_connect_timeout))
            }
        }
        mainHandler.postDelayed(connectTimeoutRunnable!!, CONNECT_TIMEOUT_MS)
    }

    private fun cancelConnectTimeout() {
        connectTimeoutRunnable?.let { mainHandler.removeCallbacks(it) }
        connectTimeoutRunnable = null
    }

    private fun stopTransmittingInternal() {
        isTransmitting = false
        audioEngine.stopRecording()
        pttClient.stopPtt()
        notifyUi { it.onPttEnded(currentUsername) }
        updateNotification()
    }

    private fun startTransmittingInternal() {
        isTransmitting = true
        audioEngine.startRecording()
        notifyUi { it.onPttGranted() }
        updateNotification()
    }

    override fun onConnected(channels: List<String>) {
        notifyUi { it.onConnected(channels) }
    }

    override fun onServerConfig(playbackGain: Float, channels: List<String>) {
        audioEngine.setPlaybackGain(playbackGain)
        notifyUi { it.onServerConfig(playbackGain, channels) }
    }

    override fun onApprovalPending(channel: String, message: String) {
        sessionPhase = SessionPhase.WAITING_APPROVAL
        currentChannel = channel
        updateNotification(message)
        notifyUi { it.onApprovalPending(channel, message) }
    }

    override fun onApprovalDenied(message: String) {
        notifyUi { it.onApprovalDenied(message) }
    }

    override fun onJoined(channel: String, users: List<String>) {
        cancelConnectTimeout()
        reconnectAttempt = 0
        cancelReconnect()
        wasEverConnected = true
        sessionPhase = SessionPhase.CONNECTED
        currentChannel = channel
        currentUsers = users
        Log.i(TAG, "Usuario unido al canal '$channel' con ${users.size} usuarios - activando PTT por volumen")
        activateVolumePtt()
        updateNotification()
        notifyUi { it.onJoined(channel, users) }
    }

    override fun onUsersUpdated(users: List<String>) {
        currentUsers = users
        notifyUi { it.onUsersUpdated(users) }
    }

    override fun onPttGranted() {
        if (pttRequested) {
            startTransmittingInternal()
        } else {
            pttClient.stopPtt()
        }
    }

    override fun onPttDenied(speaker: String) {
        pttRequested = false
        audioEngine.playPttTone(AudioEngine.PttTone.BUSY)
        currentSpeaker = speaker
        notifyUi { it.onPttDenied(speaker) }
        updateNotification()
    }

    override fun onPttStarted(username: String) {
        currentSpeaker = username
        audioEngine.refreshVolume()
        if (isRemoteSpeaker(username)) {
            lastRemoteOpeningAtMs = System.currentTimeMillis()
            audioEngine.playPttTone(AudioEngine.PttTone.OPENING)
        }
        updateNotification()
        notifyUi { it.onPttStarted(username) }
    }

    override fun onPttEnded(username: String) {
        if (isRemoteSpeaker(username)) {
            if (!isTransmitting && shouldPlayListenerRoger()) {
                audioEngine.playPttTone(AudioEngine.PttTone.ROGER_RX)
            }
            currentSpeaker = null
            updateNotification()
        } else if (!isTransmitting && !pttRequested) {
            currentSpeaker = null
            updateNotification()
        }
        notifyUi { it.onPttEnded(username) }
    }

    private fun isRemoteSpeaker(username: String): Boolean {
        return !username.equals(currentUsername, ignoreCase = true)
    }

    private fun shouldPlayListenerRoger(): Boolean {
        if (lastRemoteOpeningAtMs <= 0L) {
            return true
        }
        return System.currentTimeMillis() - lastRemoteOpeningAtMs >= AudioConfig.TONE_ROGER_MIN_AFTER_OPEN_MS
    }

    override fun onAudioReceived(data: ByteArray) {
        audioEngine.playIncoming(data)
    }

    override fun onError(message: String) {
        notifyUi { it.onError(message) }
    }

    override fun onDestroy() {
        networkMonitor.stop()
        deactivateVolumePtt()
        cancelReconnect()
        cancelConnectTimeout()
        releaseWakeLock()
        releaseWifiLock()
        if (::audioEngine.isInitialized) {
            audioEngine.release()
        }
        scope.cancel()
        super.onDestroy()
    }

    private fun syncStateTo(listener: UiListener) {
        when (sessionPhase) {
            SessionPhase.CONNECTED -> {
                val channel = currentChannel ?: return
                listener.onJoined(channel, currentUsers)
                currentSpeaker?.let { listener.onPttStarted(it) }
                if (isTransmitting) listener.onPttGranted()
            }

            SessionPhase.CONNECTING -> listener.onConnecting()

            SessionPhase.WAITING_APPROVAL -> {
                val channel = currentChannel ?: return
                listener.onApprovalPending(
                    channel,
                    getString(R.string.status_waiting_approval, channel)
                )
            }

            SessionPhase.RECONNECTING -> listener.onReconnecting(reconnectAttempt.coerceAtLeast(1))

            SessionPhase.IDLE -> Unit
        }
    }

    private fun notifyUi(block: (UiListener) -> Unit) {
        uiListeners.forEach { listener ->
            try {
                block(listener)
            } catch (e: Exception) {
                Log.e(TAG, "Error notificando UI", e)
            }
        }
    }

    private fun acquireWakeLock() {
        if (wakeLock?.isHeld == true) return
        val pm = getSystemService(POWER_SERVICE) as PowerManager
        wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "PTTComunicacion::Connection").apply {
            acquire(WAKELOCK_TIMEOUT_MS)
        }
    }

    private fun releaseWakeLock() {
        wakeLock?.let { if (it.isHeld) it.release() }
        wakeLock = null
    }

    @Suppress("DEPRECATION")
    private fun acquireWifiLock() {
        if (wifiLock?.isHeld == true) return
        val wm = applicationContext.getSystemService(WIFI_SERVICE) as WifiManager
        wifiLock = wm.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "PTTComunicacion::Wifi").apply {
            setReferenceCounted(false)
            acquire()
        }
    }

    @Suppress("DEPRECATION")
    private fun releaseWifiLock() {
        wifiLock?.let { if (it.isHeld) it.release() }
        wifiLock = null
    }

    private fun stopSelfSafely() {
        try {
            stopForeground(STOP_FOREGROUND_REMOVE)
        } catch (_: Exception) {
        }
        stopSelf()
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val channel = NotificationChannel(
            CHANNEL_ID,
            getString(R.string.notification_channel_name),
            NotificationManager.IMPORTANCE_LOW
        ).apply {
            description = getString(R.string.notification_channel_desc)
            setShowBadge(false)
        }
        getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
    }

    private fun buildNotification(text: String, channel: String?): Notification {
        val openIntent = PendingIntent.getActivity(
            this, 0, Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val disconnectIntent = PendingIntent.getService(
            this, 1,
            Intent(this, PttForegroundService::class.java).apply { action = ACTION_DISCONNECT },
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val pttToggleIntent = PendingIntent.getService(
            this, 2,
            Intent(this, PttForegroundService::class.java).apply { action = ACTION_PTT_TOGGLE },
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val title = if (channel.isNullOrBlank()) getString(R.string.app_name)
        else getString(R.string.notification_title, channel)

        val remoteViews = RemoteViews(packageName, R.layout.notification_connection)
        remoteViews.setTextViewText(R.id.notification_title, title)
        remoteViews.setTextViewText(R.id.notification_text, text)
        remoteViews.setOnClickPendingIntent(R.id.notification_disconnect, disconnectIntent)

        if (sessionPhase == SessionPhase.CONNECTED) {
            remoteViews.setViewVisibility(R.id.notification_ptt_button, View.VISIBLE)
            val pttBackground = if (isTransmitting || pttRequested) {
                R.drawable.bg_notification_ptt_button_active
            } else {
                R.drawable.bg_notification_ptt_button
            }
            val pttLabel = if (isTransmitting || pttRequested) {
                getString(R.string.notification_ptt_stop)
            } else {
                getString(R.string.notification_ptt_talk)
            }
            remoteViews.setTextViewText(R.id.notification_ptt_button, pttLabel)
            remoteViews.setInt(
                R.id.notification_ptt_button,
                "setBackgroundResource",
                pttBackground
            )
            remoteViews.setOnClickPendingIntent(R.id.notification_ptt_button, pttToggleIntent)
        } else {
            remoteViews.setViewVisibility(R.id.notification_ptt_button, View.GONE)
        }

        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_stat_notify)
            .setContentIntent(openIntent)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setCategory(NotificationCompat.CATEGORY_SERVICE)
            .setStyle(NotificationCompat.DecoratedCustomViewStyle())
            .setCustomContentView(remoteViews)
            .setCustomBigContentView(remoteViews)
            .build()
    }

    private fun foregroundServiceTypes(): Int {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return 0
        return ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE or
            ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK
    }

    private fun promoteToForeground(text: String, channel: String?) {
        val notification = buildNotification(text, channel)
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                ServiceCompat.startForeground(
                    this, NOTIFICATION_ID, notification,
                    foregroundServiceTypes()
                )
            } else {
                startForeground(NOTIFICATION_ID, notification)
            }
        } catch (e: Exception) {
            Log.e(TAG, "Error en startForeground", e)
            startForeground(NOTIFICATION_ID, notification)
        }
    }

    private fun updateNotification(customText: String? = null) {
        if (sessionPhase == SessionPhase.IDLE) return
        val text = customText
            ?: currentSpeaker?.let { getString(R.string.notification_speaking, it) }
            ?: getString(R.string.notification_listening)
        val manager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        manager.notify(NOTIFICATION_ID, buildNotification(text, currentChannel))
    }

    data class SessionSnapshot(
        val phase: SessionPhase,
        val channel: String?,
        val users: List<String>,
        val speaker: String?,
        val transmitting: Boolean,
    )

    companion object {
        private const val TAG = "PttForegroundService"
        private const val CHANNEL_ID = "ptt_connection"
        private const val NOTIFICATION_ID = 1001
        private const val WAKELOCK_TIMEOUT_MS = 10 * 60 * 60 * 1000L
        private const val CONNECT_TIMEOUT_MS = 15_000L
        private const val MAX_RECONNECT_ATTEMPTS = 12
        private val RECONNECT_DELAYS_MS = longArrayOf(2000, 3000, 5000, 8000, 10000, 15000)

        const val ACTION_CONNECT = "com.comunicacion.ptt.CONNECT"
        const val ACTION_DISCONNECT = "com.comunicacion.ptt.DISCONNECT"
        const val ACTION_PTT_TOGGLE = "com.comunicacion.ptt.PTT_TOGGLE"
        const val EXTRA_SERVER_IP = "server_ip"
        const val EXTRA_CHANNEL = "channel"
        const val EXTRA_USERNAME = "username"
    }
}
