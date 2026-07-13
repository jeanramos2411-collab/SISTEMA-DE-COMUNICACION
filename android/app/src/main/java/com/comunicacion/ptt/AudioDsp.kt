package com.comunicacion.ptt

import kotlin.math.abs
import kotlin.math.sqrt
import kotlin.math.tanh

/**
 * Procesamiento digital de audio PTT (PCM 16-bit little-endian):
 *  - AGC de captura: nivela el volumen de quien habla (sube los que hablan bajo).
 *  - Limitador suave: aplica ganancia evitando el recorte duro que distorsiona.
 *
 * Pensado para un solo hilo a la vez (PTT es medio-duplex: o se graba o se reproduce).
 */
class AudioDsp {

    private var agcGain = 1.0f

    /** Reinicia el estado del AGC (al empezar una nueva transmision). */
    fun reset() {
        agcGain = 1.0f
    }

    /**
     * AGC de captura. Mide el nivel del frame y lo acerca a un objetivo, con
     * compuerta de ruido (no amplifica silencios) y suavizado (evita "bombeo").
     */
    fun applyCaptureAgc(input: ByteArray): ByteArray {
        val samples = input.size / 2
        if (samples == 0) return input

        var sumSquares = 0.0
        var i = 0
        while (i + 1 < input.size) {
            val sample = readSample(input, i)
            sumSquares += (sample * sample).toDouble()
            i += 2
        }
        val rms = sqrt(sumSquares / samples).toFloat()

        val targetGain = if (rms < AudioConfig.AGC_NOISE_GATE_RMS) {
            1.0f
        } else {
            (AudioConfig.AGC_TARGET_RMS / rms)
                .coerceIn(AudioConfig.AGC_MIN_GAIN, AudioConfig.AGC_MAX_GAIN)
        }

        val smoothing = if (targetGain > agcGain) AudioConfig.AGC_ATTACK else AudioConfig.AGC_RELEASE
        agcGain += (targetGain - agcGain) * smoothing

        return applyGainLimited(input, agcGain)
    }

    /** Aplica ganancia con limitador suave; near-lineal hasta el 80% de la escala. */
    fun applyGainLimited(input: ByteArray, gain: Float): ByteArray {
        if (gain == 1.0f) return input

        val out = ByteArray(input.size)
        var i = 0
        while (i + 1 < input.size) {
            val raw = readSample(input, i)
            val limited = softLimit(raw * gain).toInt()
            out[i] = (limited and 0xFF).toByte()
            out[i + 1] = ((limited shr 8) and 0xFF).toByte()
            i += 2
        }
        return out
    }

    private fun readSample(buffer: ByteArray, index: Int): Int {
        val lo = buffer[index].toInt() and 0xFF
        val hi = buffer[index + 1].toInt() and 0xFF
        return ((hi shl 8) or lo).toShort().toInt()
    }

    private fun softLimit(value: Float): Float {
        val ax = abs(value)
        if (ax <= LIMIT_THRESHOLD) return value.coerceIn(-FULL_SCALE, FULL_SCALE)
        val sign = if (value >= 0f) 1f else -1f
        val over = (ax - LIMIT_THRESHOLD) / (FULL_SCALE - LIMIT_THRESHOLD)
        return sign * (LIMIT_THRESHOLD + (FULL_SCALE - LIMIT_THRESHOLD) * tanh(over))
    }

    companion object {
        private const val FULL_SCALE = 32767f
        private const val LIMIT_THRESHOLD = FULL_SCALE * 0.8f
    }
}
