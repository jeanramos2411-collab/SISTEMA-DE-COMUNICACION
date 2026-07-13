package com.comunicacion.ptt

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.hardware.display.DisplayManager
import android.media.AudioManager
import android.media.VolumeProvider
import android.media.session.MediaSession
import android.media.session.PlaybackState
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.PowerManager
import android.util.Log
import android.view.Display

/**
 * PTT con volumen ABAJO solo cuando la pantalla esta apagada.
 */
class PttVolumeKeyController(
    context: Context,
    private val onPttStart: () -> Unit,
    private val onPttStop: () -> Unit
) {
    private val appContext = context.applicationContext
    private val handler = Handler(Looper.getMainLooper())
    private val powerManager =
        appContext.getSystemService(Context.POWER_SERVICE) as PowerManager
    private val displayManager =
        appContext.getSystemService(Context.DISPLAY_SERVICE) as DisplayManager

    private var mediaSession: MediaSession? = null
    private var monitoring = false
    private var volumeHeld = false

    private val releaseRunnable = Runnable {
        volumeHeld = false
        onPttStop()
    }

    private val screenReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            when (intent?.action) {
                Intent.ACTION_SCREEN_OFF -> {
                    Log.i(TAG, "SCREEN_OFF detectado - activando MediaSession")
                    handler.post { syncScreenState() }
                }
                Intent.ACTION_SCREEN_ON -> {
                    Log.i(TAG, "SCREEN_ON detectado - desactivando MediaSession")
                    handler.post { syncScreenState() }
                }
            }
        }
    }

    private val displayListener = object : DisplayManager.DisplayListener {
        override fun onDisplayAdded(displayId: Int) = Unit

        override fun onDisplayRemoved(displayId: Int) = Unit

        override fun onDisplayChanged(displayId: Int) {
            if (displayId != Display.DEFAULT_DISPLAY) return
            handler.post { syncScreenState() }
        }
    }

    fun activate() {
        if (monitoring) return
        monitoring = true

        val filter = IntentFilter().apply {
            addAction(Intent.ACTION_SCREEN_OFF)
            addAction(Intent.ACTION_SCREEN_ON)
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            appContext.registerReceiver(screenReceiver, filter, Context.RECEIVER_NOT_EXPORTED)
        } else {
            @Suppress("DEPRECATION")
            appContext.registerReceiver(screenReceiver, filter)
        }

        displayManager.registerDisplayListener(displayListener, handler)
        syncScreenState()
        Log.i(TAG, "PTT por volumen ACTIVADO - monitoreando estado de pantalla")
    }

    fun deactivate() {
        if (!monitoring) return
        monitoring = false

        try {
            appContext.unregisterReceiver(screenReceiver)
        } catch (_: Exception) {
        }

        displayManager.unregisterDisplayListener(displayListener)
        deactivateMediaSession(stopPtt = true)
        Log.i(TAG, "PTT por volumen DESACTIVADO")
    }

    private fun syncScreenState() {
        if (!monitoring) return
        val screenOff = isScreenOff()
        Log.i(TAG, "SyncScreenState - pantalla apagada: $screenOff, mediaSession activo: ${mediaSession != null}")
        if (screenOff) {
            onScreenOff()
        } else {
            onScreenOn()
        }
    }

    private fun onScreenOff() {
        if (!monitoring) return
        Log.i(TAG, "onScreenOff() - activando MediaSession para PTT por volumen")
        activateMediaSession()
    }

    private fun onScreenOn() {
        Log.i(TAG, "onScreenOn() - desactivando MediaSession")
        deactivateMediaSession(stopPtt = true)
    }

    private fun isScreenOff(): Boolean {
        val display = displayManager.getDisplay(Display.DEFAULT_DISPLAY)
        return display.state == Display.STATE_OFF || !powerManager.isInteractive
    }

    private fun activateMediaSession() {
        if (mediaSession != null) {
            Log.w(TAG, "MediaSession ya está activo, no recreando")
            return
        }

        try {
            Log.i(TAG, "Creando MediaSession para PTT por volumen...")
            mediaSession = MediaSession(appContext, "PTTComunicacionVolume").apply {
                setFlags(
                    MediaSession.FLAG_HANDLES_MEDIA_BUTTONS or
                        MediaSession.FLAG_HANDLES_TRANSPORT_CONTROLS
                )

                val volumeProvider = object : VolumeProvider(
                    VolumeProvider.VOLUME_CONTROL_RELATIVE,
                    100,
                    50
                ) {
                    override fun onAdjustVolume(direction: Int) {
                        Log.i(TAG, "onAdjustVolume llamado - direction: $direction, monitoring: $monitoring, screenOff: ${isScreenOff()}")
                        if (direction == AudioManager.ADJUST_LOWER) {
                            onVolumeDownEvent()
                        }
                    }
                }
                setPlaybackToRemote(volumeProvider)

                setPlaybackState(
                    PlaybackState.Builder()
                        .setActions(
                            PlaybackState.ACTION_PLAY or
                                PlaybackState.ACTION_PAUSE or
                                PlaybackState.ACTION_PLAY_PAUSE
                        )
                        .setState(PlaybackState.STATE_PLAYING, 0, 1f)
                        .build()
                )
                isActive = true
            }
            Log.i(TAG, "MediaSession creado y activado exitosamente para PTT por volumen")
        } catch (e: Exception) {
            Log.e(TAG, "Error al crear MediaSession para PTT por volumen", e)
        }
    }

    private fun deactivateMediaSession(stopPtt: Boolean) {
        handler.removeCallbacks(releaseRunnable)
        if (stopPtt && volumeHeld) {
            volumeHeld = false
            onPttStop()
        }

        try {
            mediaSession?.isActive = false
            mediaSession?.release()
        } catch (_: Exception) {
        }
        mediaSession = null
    }

    private fun onVolumeDownEvent() {
        Log.i(TAG, "onVolumeDownEvent() - monitoring: $monitoring, screenOff: ${isScreenOff()}, volumeHeld: $volumeHeld")
        if (!monitoring || !isScreenOff()) {
            Log.w(TAG, "onVolumeDownEvent ignorado - monitoring: $monitoring, screenOff: ${isScreenOff()}")
            return
        }
        handler.removeCallbacks(releaseRunnable)
        if (!volumeHeld) {
            volumeHeld = true
            Log.i(TAG, "INICIANDO PTT por volumen")
            onPttStart()
        } else {
            Log.i(TAG, "Renovando timeout de PTT por volumen")
        }
        handler.postDelayed(releaseRunnable, RELEASE_DELAY_MS)
    }

    companion object {
        private const val TAG = "PttVolumeKeyController"
        private const val RELEASE_DELAY_MS = 2000L // Aumentado de 350ms a 2000ms para facilitar uso
    }
}
