package com.comunicacion.ptt

import android.content.Context

class Prefs(context: Context) {
    private val prefs = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)

    var serverIp: String
        get() = prefs.getString(KEY_SERVER_IP, DEFAULT_IP) ?: DEFAULT_IP
        set(value) = prefs.edit().putString(KEY_SERVER_IP, value).apply()

    var username: String
        get() = prefs.getString(KEY_USERNAME, "") ?: ""
        set(value) = prefs.edit().putString(KEY_USERNAME, value).apply()

    var lastChannel: String
        get() = prefs.getString(KEY_CHANNEL, "") ?: ""
        set(value) = prefs.edit().putString(KEY_CHANNEL, value).apply()

    /** IP que la app ya valido contra el servidor PTT. */
    var verifiedServerIp: String
        get() = prefs.getString(KEY_VERIFIED_IP, "") ?: ""
        set(value) = prefs.edit().putString(KEY_VERIFIED_IP, value.trim()).apply()

    var cachedChannels: List<String>
        get() {
            val raw = prefs.getString(KEY_CACHED_CHANNELS, "") ?: ""
            if (raw.isBlank()) return emptyList()
            return raw.split(CHANNEL_SEP).filter { it.isNotBlank() }
        }
        set(value) {
            prefs.edit()
                .putString(KEY_CACHED_CHANNELS, value.joinToString(CHANNEL_SEP))
                .apply()
        }

    fun isVerifiedFor(ip: String): Boolean {
        val trimmed = ip.trim()
        return trimmed.isNotBlank() && trimmed == verifiedServerIp
    }

    fun clearVerification() {
        prefs.edit()
            .remove(KEY_VERIFIED_IP)
            .remove(KEY_CACHED_CHANNELS)
            .apply()
    }

    var autoVolumeControl: Boolean
        get() = prefs.getBoolean(KEY_AUTO_VOLUME_CONTROL, false)
        set(value) = prefs.edit().putBoolean(KEY_AUTO_VOLUME_CONTROL, value).apply()

    companion object {
        private const val PREFS_NAME = "ptt_prefs"
        private const val KEY_SERVER_IP = "server_ip"
        private const val KEY_USERNAME = "username"
        private const val KEY_CHANNEL = "channel"
        private const val KEY_VERIFIED_IP = "verified_server_ip"
        private const val KEY_CACHED_CHANNELS = "cached_channels"
        private const val KEY_AUTO_VOLUME_CONTROL = "auto_volume_control"
        private const val CHANNEL_SEP = "\u001F"
        private const val DEFAULT_IP = "192.168.0.101"
    }
}
