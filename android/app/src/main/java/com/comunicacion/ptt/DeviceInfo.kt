package com.comunicacion.ptt

import android.content.Context
import android.net.wifi.WifiManager
import android.os.Build
import android.provider.Settings
import java.net.NetworkInterface

object DeviceInfo {

    fun getDeviceId(context: Context): String {
        return Settings.Secure.getString(context.contentResolver, Settings.Secure.ANDROID_ID)
            ?.takeIf { it.isNotBlank() && it != "unknown" }
            ?: "dev-${System.currentTimeMillis()}"
    }

    fun getMacAddress(context: Context): String {
        try {
            if (Build.VERSION.SDK_INT < Build.VERSION_CODES.M) {
                @Suppress("DEPRECATION")
                val wifi = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
                @Suppress("DEPRECATION")
                val mac = wifi.connectionInfo?.macAddress
                if (!mac.isNullOrBlank() && mac != "02:00:00:00:00:00") {
                    return mac.uppercase()
                }
            }
            NetworkInterface.getNetworkInterfaces()?.toList()?.forEach { nif ->
                if (nif.name.equals("wlan0", ignoreCase = true)) {
                    val mac = nif.hardwareAddress ?: return@forEach
                    if (mac.isNotEmpty()) {
                        return mac.joinToString(":") { byte -> "%02X".format(byte) }
                    }
                }
            }
        } catch (_: Exception) {
        }
        return ""
    }
}
