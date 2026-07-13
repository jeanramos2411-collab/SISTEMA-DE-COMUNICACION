package com.comunicacion.ptt

import android.Manifest
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.ServiceConnection
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.os.IBinder
import android.text.Editable
import android.text.TextWatcher
import android.view.MotionEvent
import android.widget.ArrayAdapter
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import com.comunicacion.ptt.databinding.ActivityMainBinding
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class MainActivity : AppCompatActivity(), PttForegroundService.UiListener {

    private lateinit var binding: ActivityMainBinding
    private lateinit var prefs: Prefs

    private var pttService: PttForegroundService? = null
    private var serviceBound = false

    private var sessionPhase = PttForegroundService.SessionPhase.IDLE
    private var isTransmitting = false
    private var channelAdapter: ArrayAdapter<String>? = null
    private var channelsRefreshJob: Job? = null
    private val uiScope = CoroutineScope(Dispatchers.Main + Job())

    private val serviceConnection = object : ServiceConnection {
        override fun onServiceConnected(name: ComponentName?, binder: IBinder?) {
            pttService = (binder as PttForegroundService.LocalBinder).getService()
            serviceBound = true
            pttService?.addUiListener(this@MainActivity)
        }

        override fun onServiceDisconnected(name: ComponentName?) {
            pttService = null
            serviceBound = false
            applyIdleUi(null)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)

        prefs = Prefs(this)
        setupUi()
        requestPermissionsIfNeeded()
        applyIdleUi(null)
        evaluateVerificationState(autoRefresh = false)
    }

    override fun onStart() {
        super.onStart()
        bindService(
            Intent(this, PttForegroundService::class.java),
            serviceConnection,
            0
        )
        if (sessionPhase == PttForegroundService.SessionPhase.IDLE) {
            evaluateVerificationState(autoRefresh = true)
        }
    }

    override fun onStop() {
        if (serviceBound) {
            pttService?.removeUiListener(this)
            unbindService(serviceConnection)
            serviceBound = false
            pttService = null
        }
        super.onStop()
    }

    override fun onDestroy() {
        channelsRefreshJob?.cancel()
        uiScope.cancel()
        
        // Nota: No forzamos stopService aquí porque onTaskRemoved() en el servicio
        // se encargará de detenerlo cuando el usuario cierre la app (swipe away)
        // Esto evita llamadas duplicadas y permite que el servicio maneje su ciclo de vida
        
        super.onDestroy()
    }

    private fun setupUi() {
        binding.serverIpInput.setText(prefs.serverIp)
        binding.usernameInput.setText(prefs.username)

        // Configurar switch de control automático de volumen
        binding.autoVolumeSwitch.isChecked = prefs.autoVolumeControl
        binding.autoVolumeSwitch.setOnCheckedChangeListener { _, isChecked ->
            prefs.autoVolumeControl = isChecked
        }

        showChannelPlaceholder()

        binding.serverIpInput.addTextChangedListener(object : TextWatcher {
            override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) = Unit
            override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) = Unit
            override fun afterTextChanged(s: Editable?) {
                if (sessionPhase != PttForegroundService.SessionPhase.IDLE) return
                evaluateVerificationState(autoRefresh = false)
            }
        })

        binding.verifyServerButton.setOnClickListener {
            verifyServer(manual = true)
        }

        binding.connectButton.setOnClickListener {
            if (isSessionActive()) {
                disconnect()
            } else {
                connect()
            }
        }

        binding.pttButton.setOnTouchListener { _, event ->
            if (sessionPhase != PttForegroundService.SessionPhase.CONNECTED) {
                return@setOnTouchListener false
            }

            when (event.action) {
                MotionEvent.ACTION_DOWN -> {
                    pttService?.startPtt()
                    true
                }

                MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                    pttService?.stopPtt()
                    true
                }

                else -> false
            }
        }
    }

    private fun currentServerIp(): String {
        return binding.serverIpInput.text?.toString()?.trim().orEmpty()
    }

    private fun isServerVerifiedForCurrentIp(): Boolean {
        return prefs.isVerifiedFor(currentServerIp())
    }

    private fun evaluateVerificationState(autoRefresh: Boolean) {
        if (sessionPhase != PttForegroundService.SessionPhase.IDLE) return

        val ip = currentServerIp()
        if (ip.isBlank()) {
            binding.verifyServerButton.visibility = android.view.View.VISIBLE
            binding.verifyServerButton.isEnabled = false
            binding.serverVerifyStatus.visibility = android.view.View.VISIBLE
            binding.serverVerifyStatus.text = getString(R.string.verify_server_hint)
            binding.connectButton.isEnabled = false
            showChannelPlaceholder()
            return
        }

        if (!isServerVerifiedForCurrentIp()) {
            binding.verifyServerButton.visibility = android.view.View.VISIBLE
            binding.verifyServerButton.isEnabled = true
            binding.serverVerifyStatus.visibility = android.view.View.VISIBLE
            binding.serverVerifyStatus.text = getString(R.string.verify_server_hint)
            binding.connectButton.isEnabled = false
            showChannelPlaceholder()
            return
        }

        binding.verifyServerButton.visibility = android.view.View.GONE
        binding.serverVerifyStatus.visibility = android.view.View.VISIBLE
        binding.connectButton.isEnabled = true

        val cached = prefs.cachedChannels
        if (cached.isNotEmpty()) {
            updateChannelList(cached, updateStatus = true)
        } else {
            showChannelPlaceholder()
        }

        if (autoRefresh) {
            refreshChannelsFromServer(silent = true)
        }
    }

    private fun verifyServer(manual: Boolean) {
        val ip = currentServerIp()
        if (ip.isBlank()) {
            toast(getString(R.string.server_ip))
            return
        }
        if (!NetworkMonitor.isNetworkAvailable(this)) {
            toast(getString(R.string.status_no_network))
            return
        }

        binding.verifyServerButton.isEnabled = false
        binding.serverVerifyStatus.visibility = android.view.View.VISIBLE
        binding.serverVerifyStatus.text = getString(R.string.verify_server_loading)

        refreshChannelsFromServer(silent = false, onVerified = { success ->
            binding.verifyServerButton.isEnabled = true
            if (success) {
                prefs.serverIp = ip
                binding.verifyServerButton.visibility = android.view.View.GONE
                binding.connectButton.isEnabled = true
            } else if (manual) {
                binding.verifyServerButton.visibility = android.view.View.VISIBLE
                binding.connectButton.isEnabled = false
            }
        })
    }

    private fun refreshChannelsFromServer(
        silent: Boolean,
        onVerified: ((Boolean) -> Unit)? = null
    ) {
        val ip = currentServerIp()
        if (ip.isBlank()) {
            onVerified?.invoke(false)
            return
        }

        channelsRefreshJob?.cancel()
        channelsRefreshJob = uiScope.launch {
            if (!silent) {
                binding.serverVerifyStatus.text = getString(
                    if (isServerVerifiedForCurrentIp()) R.string.channels_updating
                    else R.string.verify_server_loading
                )
            }

            val result = withContext(Dispatchers.IO) {
                ServerDirectory.fetch(ip)
            }

            result.fold(
                onSuccess = { info ->
                    prefs.serverIp = ip
                    prefs.verifiedServerIp = ip
                    prefs.cachedChannels = info.channels
                    updateChannelList(info.channels, updateStatus = true)
                    binding.connectButton.isEnabled = sessionPhase == PttForegroundService.SessionPhase.IDLE
                    onVerified?.invoke(true)
                },
                onFailure = { error ->
                    if (!silent) {
                        toast(error.message ?: getString(R.string.error_connection))
                        binding.serverVerifyStatus.text = getString(R.string.verify_server_hint)
                    }
                    onVerified?.invoke(false)
                }
            )
        }
    }

    private fun showChannelPlaceholder() {
        channelAdapter = ArrayAdapter(
            this,
            android.R.layout.simple_spinner_dropdown_item,
            listOf(getString(R.string.channels_placeholder))
        )
        binding.channelSpinner.adapter = channelAdapter
        binding.channelSpinner.setSelection(0)
        binding.channelSpinner.isEnabled = false
    }

    private fun isSessionActive(): Boolean {
        return sessionPhase == PttForegroundService.SessionPhase.CONNECTING ||
            sessionPhase == PttForegroundService.SessionPhase.WAITING_APPROVAL ||
            sessionPhase == PttForegroundService.SessionPhase.CONNECTED ||
            sessionPhase == PttForegroundService.SessionPhase.RECONNECTING
    }

    private fun isPttAllowed(): Boolean {
        return sessionPhase == PttForegroundService.SessionPhase.CONNECTED
    }

    private fun connect() {
        val serverIp = currentServerIp()
        val username = binding.usernameInput.text?.toString()?.trim().orEmpty()
        val channel = binding.channelSpinner.selectedItem?.toString().orEmpty()

        if (serverIp.isBlank()) {
            toast(getString(R.string.server_ip))
            return
        }
        if (!isServerVerifiedForCurrentIp()) {
            toast(getString(R.string.verify_server_required))
            evaluateVerificationState(autoRefresh = false)
            return
        }
        if (channel.isBlank() || channel == getString(R.string.channels_placeholder)) {
            toast(getString(R.string.verify_server_required))
            return
        }
        if (username.isBlank()) {
            toast(getString(R.string.username))
            return
        }
        if (!NetworkMonitor.isNetworkAvailable(this)) {
            toast(getString(R.string.status_no_network))
            return
        }
        if (!hasMicPermission()) {
            requestPermissionsIfNeeded()
            return
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !hasNotificationPermission()) {
            requestPermissionsIfNeeded()
            return
        }

        prefs.serverIp = serverIp
        prefs.username = username
        prefs.lastChannel = channel

        applyConnectingUi()

        val intent = Intent(this, PttForegroundService::class.java).apply {
            action = PttForegroundService.ACTION_CONNECT
            putExtra(PttForegroundService.EXTRA_SERVER_IP, serverIp)
            putExtra(PttForegroundService.EXTRA_CHANNEL, channel)
            putExtra(PttForegroundService.EXTRA_USERNAME, username)
        }
        ContextCompat.startForegroundService(this, intent)

        if (!serviceBound) {
            bindService(Intent(this, PttForegroundService::class.java), serviceConnection, 0)
        }
    }

    private fun disconnect() {
        if (isTransmitting) {
            applyTransmittingUi(false)
        }
        applyIdleUi(null)
        startService(
            Intent(this, PttForegroundService::class.java).apply {
                action = PttForegroundService.ACTION_DISCONNECT
            }
        )
    }

    private fun applyIdleUi(message: String?) {
        sessionPhase = PttForegroundService.SessionPhase.IDLE
        isTransmitting = false
        binding.connectButton.isEnabled = isServerVerifiedForCurrentIp()
        binding.connectButton.text = getString(R.string.connect)
        binding.serverIpInput.isEnabled = true
        binding.usernameInput.isEnabled = true
        binding.statusText.text = getString(R.string.status_disconnected)
        binding.usersText.text = "-"
        binding.speakerText.text = ""
        applyPttButtonState()
        evaluateVerificationState(autoRefresh = true)
        message?.let { toast(it) }
    }

    private fun applyConnectingUi() {
        sessionPhase = PttForegroundService.SessionPhase.CONNECTING
        binding.connectButton.isEnabled = true
        binding.connectButton.text = getString(R.string.disconnect)
        binding.serverIpInput.isEnabled = false
        binding.usernameInput.isEnabled = false
        binding.channelSpinner.isEnabled = false
        binding.verifyServerButton.visibility = android.view.View.GONE
        binding.statusText.text = getString(R.string.status_connecting)
        applyPttButtonState()
    }

    private fun applyReconnectingUi(attempt: Int) {
        sessionPhase = PttForegroundService.SessionPhase.RECONNECTING
        binding.connectButton.isEnabled = true
        binding.connectButton.text = getString(R.string.disconnect)
        binding.serverIpInput.isEnabled = false
        binding.usernameInput.isEnabled = false
        binding.channelSpinner.isEnabled = false
        binding.statusText.text = if (attempt > 0) {
            getString(R.string.status_reconnecting_attempt, attempt)
        } else {
            getString(R.string.status_reconnecting)
        }
        binding.pttButton.isEnabled = false
        applyPttButtonState()
    }

    private fun applyConnectedUi(channel: String, users: List<String>) {
        sessionPhase = PttForegroundService.SessionPhase.CONNECTED
        binding.connectButton.isEnabled = true
        binding.connectButton.text = getString(R.string.disconnect)
        binding.serverIpInput.isEnabled = false
        binding.usernameInput.isEnabled = false
        binding.channelSpinner.isEnabled = false
        binding.statusText.text = getString(R.string.status_connected, channel)
        binding.usersText.text = if (users.isEmpty()) "-" else users.joinToString(", ")
        applyPttButtonState()
    }

    private fun applyTransmittingUi(transmitting: Boolean) {
        isTransmitting = transmitting
        if (transmitting) {
            binding.pttButton.text = getString(R.string.ptt_active)
            binding.pttButton.backgroundTintList =
                ContextCompat.getColorStateList(this, R.color.ptt_active)
        } else {
            binding.pttButton.text = getString(R.string.ptt_hold)
            binding.pttButton.backgroundTintList =
                ContextCompat.getColorStateList(this, R.color.ptt_idle)
        }
    }

    private fun applyPttButtonState() {
        val allowed = isPttAllowed()
        binding.pttButton.isEnabled = allowed
        if (!allowed) {
            binding.pttButton.text = getString(R.string.ptt_hold)
            binding.pttButton.backgroundTintList =
                ContextCompat.getColorStateList(this, R.color.ptt_disabled)
        } else if (!isTransmitting) {
            binding.pttButton.backgroundTintList =
                ContextCompat.getColorStateList(this, R.color.ptt_idle)
        }
    }

    override fun onConnected(channels: List<String>) {
        runOnUiThread { applyChannelUpdate(channels) }
    }

    override fun onServerConfig(playbackGain: Float, channels: List<String>) {
        runOnUiThread { applyChannelUpdate(channels) }
    }

    private fun applyChannelUpdate(channels: List<String>) {
        if (channels.isEmpty()) return
        prefs.cachedChannels = channels
        updateChannelList(channels, updateStatus = sessionPhase == PttForegroundService.SessionPhase.IDLE)
    }

    override fun onApprovalPending(channel: String, message: String) {
        runOnUiThread { applyWaitingApprovalUi(channel, message) }
    }

    override fun onApprovalDenied(message: String) {
        runOnUiThread { applyIdleUi(message) }
    }

    private fun applyWaitingApprovalUi(channel: String, message: String) {
        sessionPhase = PttForegroundService.SessionPhase.WAITING_APPROVAL
        binding.connectButton.isEnabled = true
        binding.connectButton.text = getString(R.string.disconnect)
        binding.serverIpInput.isEnabled = false
        binding.usernameInput.isEnabled = false
        binding.channelSpinner.isEnabled = false
        binding.statusText.text = getString(R.string.status_waiting_approval, channel)
        binding.usersText.text = message
        binding.speakerText.text = ""
        applyPttButtonState()
    }

    private fun updateChannelList(channels: List<String>, updateStatus: Boolean) {
        if (channels.isEmpty()) return
        channelAdapter = ArrayAdapter(this, android.R.layout.simple_spinner_dropdown_item, channels)
        binding.channelSpinner.adapter = channelAdapter
        binding.channelSpinner.isEnabled = sessionPhase == PttForegroundService.SessionPhase.IDLE
        val selection = channels.indexOf(prefs.lastChannel).coerceAtLeast(0)
        binding.channelSpinner.setSelection(selection)
        if (updateStatus) {
            binding.serverVerifyStatus.visibility = android.view.View.VISIBLE
            binding.serverVerifyStatus.text = getString(R.string.verify_server_ok, channels.size)
        }
    }

    override fun onJoined(channel: String, users: List<String>) {
        runOnUiThread { applyConnectedUi(channel, users) }
    }

    override fun onUsersUpdated(users: List<String>) {
        runOnUiThread {
            binding.usersText.text = if (users.isEmpty()) "-" else users.joinToString(", ")
        }
    }

    override fun onConnecting() {
        runOnUiThread { applyConnectingUi() }
    }

    override fun onReconnecting(attempt: Int) {
        runOnUiThread {
            if (!NetworkMonitor.isNetworkAvailable(this)) {
                sessionPhase = PttForegroundService.SessionPhase.RECONNECTING
                binding.connectButton.isEnabled = true
                binding.connectButton.text = getString(R.string.disconnect)
                binding.statusText.text = getString(R.string.status_no_network)
                binding.pttButton.isEnabled = false
                applyPttButtonState()
            } else {
                applyReconnectingUi(attempt)
            }
        }
    }

    override fun onPttGranted() {
        runOnUiThread { applyTransmittingUi(true) }
    }

    override fun onPttDenied(speaker: String) {
        runOnUiThread {
            applyTransmittingUi(false)
            toast(getString(R.string.channel_busy, speaker))
        }
    }

    override fun onPttStarted(username: String) {
        runOnUiThread {
            binding.speakerText.text = getString(R.string.ptt_listening, username)
        }
    }

    override fun onPttEnded(username: String) {
        runOnUiThread {
            applyTransmittingUi(false)
            if (sessionPhase == PttForegroundService.SessionPhase.CONNECTED) {
                binding.speakerText.text = ""
            }
        }
    }

    override fun onSessionEnded(message: String?) {
        runOnUiThread { applyIdleUi(message) }
    }

    override fun onError(message: String) {
        runOnUiThread { toast(message) }
    }

    private fun hasMicPermission(): Boolean {
        return ContextCompat.checkSelfPermission(this, Manifest.permission.RECORD_AUDIO) ==
            PackageManager.PERMISSION_GRANTED
    }

    private fun hasNotificationPermission(): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return true
        return ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) ==
            PackageManager.PERMISSION_GRANTED
    }

    private fun requestPermissionsIfNeeded() {
        val needed = mutableListOf<String>()
        if (!hasMicPermission()) needed.add(Manifest.permission.RECORD_AUDIO)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !hasNotificationPermission()) {
            needed.add(Manifest.permission.POST_NOTIFICATIONS)
        }
        if (needed.isNotEmpty()) {
            ActivityCompat.requestPermissions(this, needed.toTypedArray(), REQUEST_PERMISSIONS)
        }
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != REQUEST_PERMISSIONS) return
        if (grantResults.isNotEmpty() && grantResults.any { it != PackageManager.PERMISSION_GRANTED }) {
            toast(getString(R.string.permission_required))
        }
    }

    private fun toast(message: String) {
        Toast.makeText(this, message, Toast.LENGTH_SHORT).show()
    }

    companion object {
        private const val REQUEST_PERMISSIONS = 1001
    }
}
