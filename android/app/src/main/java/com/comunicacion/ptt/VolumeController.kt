package com.comunicacion.ptt

import android.content.Context
import android.media.AudioDeviceInfo
import android.media.AudioManager
import android.os.Build
import android.util.Log

/**
 * Sube el volumen de la app al maximo y fuerza salida por altavoz.
 * Android maneja volumen de "llamada/comunicacion" aparte del volumen de musica;
 * en muchos telefonos ese canal esta muy bajo sin que el usuario lo note.
 */
class VolumeController(context: Context) {

    private val audioManager = context.getSystemService(Context.AUDIO_SERVICE) as AudioManager

    private var previousMode: Int = AudioManager.MODE_NORMAL
    private var previousSpeakerOn: Boolean = false
    private var savedVolumes: Map<Int, Int> = emptyMap()
    private var active = false
    private var autoVolumeEnabled: Boolean = false

    fun boostForPtt(autoControl: Boolean = false) {
        if (active) return
        active = true
        autoVolumeEnabled = autoControl

        previousMode = audioManager.mode
        @Suppress("DEPRECATION")
        previousSpeakerOn = audioManager.isSpeakerphoneOn

        val streams = listOf(
            AudioManager.STREAM_MUSIC,
            AudioManager.STREAM_VOICE_CALL,
        )

        savedVolumes = streams.associateWith { stream ->
            audioManager.getStreamVolume(stream)
        }

        audioManager.mode = AudioManager.MODE_IN_COMMUNICATION
        routeToSpeaker()

        // Solo maximizar volumen si el control automático está habilitado
        if (autoVolumeEnabled) {
            streams.forEach { stream ->
                val max = audioManager.getStreamMaxVolume(stream)
                if (max > 0) {
                    audioManager.setStreamVolume(stream, max, 0)
                }
            }
            Log.i(TAG, "Volumen multimedia al maximo para PTT. Musica=${volumeLabel(AudioManager.STREAM_MUSIC)}")
        } else {
            Log.i(TAG, "Control de volumen manual: respetando configuracion del usuario")
        }
    }

    /** Vuelve a aplicar volumen maximo ( algunos telefonos lo bajan solos ). */
    fun reapplyMaxVolume() {
        if (!active) return
        routeToSpeaker()
        
        // Solo maximizar si el control automático está habilitado
        if (autoVolumeEnabled) {
            listOf(
                AudioManager.STREAM_MUSIC,
                AudioManager.STREAM_VOICE_CALL,
            ).forEach { stream ->
                val max = audioManager.getStreamMaxVolume(stream)
                if (max > 0) {
                    audioManager.setStreamVolume(stream, max, 0)
                }
            }
        }
    }

    fun restore() {
        if (!active) return
        active = false

        savedVolumes.forEach { (stream, level) ->
            audioManager.setStreamVolume(stream, level, 0)
        }

        @Suppress("DEPRECATION")
        audioManager.isSpeakerphoneOn = previousSpeakerOn
        audioManager.mode = previousMode

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            audioManager.clearCommunicationDevice()
        }

        Log.i(TAG, "Volumen del sistema restaurado")
    }

    private fun routeToSpeaker() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            val speaker = audioManager.availableCommunicationDevices
                .firstOrNull { it.type == AudioDeviceInfo.TYPE_BUILTIN_SPEAKER }
            if (speaker != null) {
                audioManager.setCommunicationDevice(speaker)
                return
            }
        }
        @Suppress("DEPRECATION")
        audioManager.isSpeakerphoneOn = true
    }

    private fun volumeLabel(stream: Int): String {
        val current = audioManager.getStreamVolume(stream)
        val max = audioManager.getStreamMaxVolume(stream)
        return "$current/$max"
    }

    companion object {
        private const val TAG = "VolumeController"
    }
}
