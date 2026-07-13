package com.comunicacion.ptt

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import java.util.concurrent.TimeUnit

object ServerDirectory {
    private const val ADMIN_PORT = 8766

    private val client = OkHttpClient.Builder()
        .connectTimeout(8, TimeUnit.SECONDS)
        .readTimeout(8, TimeUnit.SECONDS)
        .build()

    data class ServerInfo(
        val channels: List<String>,
        val audioFormat: String,
    )

    suspend fun fetch(serverIp: String): Result<ServerInfo> = withContext(Dispatchers.IO) {
        val ip = serverIp.trim()
        if (ip.isBlank()) {
            return@withContext Result.failure(IllegalArgumentException("IP vacia"))
        }

        try {
            val url = "http://$ip:$ADMIN_PORT/api/public/info"
            val request = Request.Builder().url(url).get().build()
            client.newCall(request).execute().use { response ->
                val body = response.body?.string().orEmpty()
                if (!response.isSuccessful) {
                    return@withContext Result.failure(
                        Exception("Servidor respondio ${response.code}")
                    )
                }

                val json = JSONObject(body)
                if (!json.optBoolean("ok", false)) {
                    return@withContext Result.failure(Exception("Respuesta invalida del servidor"))
                }
                if (json.optString("service") != "ptt-comunicacion") {
                    return@withContext Result.failure(Exception("No es un servidor PTT"))
                }

                val channelsArr = json.getJSONArray("channels")
                val channels = buildList {
                    for (i in 0 until channelsArr.length()) {
                        val name = channelsArr.optString(i, "").trim()
                        if (name.isNotEmpty()) add(name)
                    }
                }
                if (channels.isEmpty()) {
                    return@withContext Result.failure(Exception("No hay bloques activos en el servidor"))
                }

                Result.success(
                    ServerInfo(
                        channels = channels,
                        audioFormat = json.optString("audio_format", "opus"),
                    )
                )
            }
        } catch (e: Exception) {
            Result.failure(
                Exception(
                    when {
                        e.message?.contains("Failed to connect", ignoreCase = true) == true ->
                            "No se alcanza el servidor. Revise IP y WiFi."
                        e.message?.contains("timeout", ignoreCase = true) == true ->
                            "Tiempo agotado al contactar el servidor."
                        else -> e.message ?: "Error de red"
                    }
                )
            )
        }
    }
}
