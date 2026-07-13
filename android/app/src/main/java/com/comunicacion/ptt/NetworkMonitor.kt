package com.comunicacion.ptt

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities

class NetworkMonitor(
    context: Context,
    private val callback: Callback
) {
    interface Callback {
        fun onNetworkAvailable()
        fun onNetworkLost()
    }

    private val appContext = context.applicationContext
    private val connectivityManager =
        appContext.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager

    private var registered = false
    private var hasValidatedNetwork = isNetworkAvailable(appContext)

    private val networkCallback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            updateNetworkState(true)
        }

        override fun onLost(network: Network) {
            if (!isNetworkAvailable(appContext)) {
                updateNetworkState(false)
            }
        }

        override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
            val online = caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) &&
                (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) ||
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) ||
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET))
            updateNetworkState(online && isNetworkAvailable(appContext))
        }
    }

    private fun updateNetworkState(online: Boolean) {
        if (online && !hasValidatedNetwork) {
            hasValidatedNetwork = true
            callback.onNetworkAvailable()
        } else if (!online && hasValidatedNetwork) {
            hasValidatedNetwork = false
            callback.onNetworkLost()
        }
    }

    fun start() {
        if (registered) return
        val request = android.net.NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()
        connectivityManager.registerNetworkCallback(request, networkCallback)
        registered = true
        hasValidatedNetwork = isNetworkAvailable(appContext)
    }

    fun stop() {
        if (!registered) return
        connectivityManager.unregisterNetworkCallback(networkCallback)
        registered = false
    }

    fun isOnline(): Boolean = isNetworkAvailable(appContext)

    companion object {
        fun isNetworkAvailable(context: Context): Boolean {
            val manager = context.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
            val network = manager.activeNetwork ?: return false
            val caps = manager.getNetworkCapabilities(network) ?: return false
            return caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) &&
                (caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) ||
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) ||
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET))
        }
    }
}
