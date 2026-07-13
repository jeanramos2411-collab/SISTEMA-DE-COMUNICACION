package com.comunicacion.ptt

object AudioConfig {
    const val SAMPLE_RATE = 16000
    const val CHANNELS = 1
    const val BITS_PER_SAMPLE = 16
    const val FRAME_MS = 40
    const val FRAME_BYTES = SAMPLE_RATE * FRAME_MS / 1000 * 2

    /** Audio PTT en PCM crudo (sin compresion); el servidor solo reenvia los bytes.
     *  Amplificacion digital del audio recibido (solo esta app, no otras apps). */
    const val PLAYBACK_GAIN = 3.0f

    /** Pitidos locales tipo radio (Hz / ms) — no van por red */
    const val TONE_OPEN_HZ = 880
    const val TONE_OPEN_MS = 120

    /** Oyente al terminar transmision remota */
    const val TONE_ROGER_RX_HZ = 620
    const val TONE_ROGER_RX_MS = 140

    const val TONE_BUSY_HZ = 440
    const val TONE_BUSY_MS = 80
    const val TONE_AMPLITUDE = 0.32f
    const val TONE_MIN_INTERVAL_MS = 250L

    /** Colchon de reproduccion para absorber jitter de red (ms de PCM acumulado).
     *  En LAN el jitter es bajo: 40 ms (1 frame) basta para arrancar sin huecos. */
    const val JITTER_BUFFER_MIN_MS = 40
    const val JITTER_BUFFER_MAX_MS = 200

    /** Tras este tiempo sin audio se considera nueva rafaga y se vuelve a primar el colchon. */
    const val JITTER_REPRIME_AFTER_MS = 200

    /** AGC de captura: nivela el volumen de quien habla (sube a los que hablan bajo). */
    const val AGC_TARGET_RMS = 5000f      // ~-16 dBFS, nivel de voz comodo
    const val AGC_NOISE_GATE_RMS = 300f   // por debajo = silencio/ruido, no amplificar
    const val AGC_MIN_GAIN = 0.7f
    const val AGC_MAX_GAIN = 8.0f
    const val AGC_ATTACK = 0.25f          // sube rapido al detectar voz
    const val AGC_RELEASE = 0.15f         // baja mas lento (evita bombeo)

    /** No roger RX en oyente si la transmision fue mas corta que esto */
    const val TONE_ROGER_MIN_AFTER_OPEN_MS = 450L
}