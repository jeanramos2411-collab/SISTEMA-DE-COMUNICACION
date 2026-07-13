package com.comunicacion.ptt

import android.content.Context
import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioRecord
import android.media.AudioTrack
import android.media.MediaRecorder
import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlin.math.PI
import kotlin.math.sin

class AudioEngine(
    context: Context,
    private val scope: CoroutineScope,
    private val onAudioFrame: (ByteArray) -> Unit,
    private val autoVolumeControl: Boolean = false
) {
    private val volumeController = VolumeController(context.applicationContext)
    private val playbackLock = Any()
    private val queueLock = Any()
    private val dsp = AudioDsp()
    private var playbackGain = AudioConfig.PLAYBACK_GAIN
    private var recordJob: Job? = null
    private var playbackWriterJob: Job? = null
    private var audioRecord: AudioRecord? = null
    private var audioTrack: AudioTrack? = null
    private var lastToneAtMs = 0L
    private val playbackQueue = ArrayDeque<ByteArray>()

    enum class PttTone {
        OPENING,
        ROGER_RX,
        BUSY,
    }

    fun startPlayback() {
        if (audioTrack != null) return

        volumeController.boostForPtt(autoVolumeControl)

        val minBuffer = AudioTrack.getMinBufferSize(
            AudioConfig.SAMPLE_RATE,
            AudioFormat.CHANNEL_OUT_MONO,
            AudioFormat.ENCODING_PCM_16BIT
        )

        audioTrack = AudioTrack.Builder()
            .setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_MEDIA)
                    .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                    .build()
            )
            .setAudioFormat(
                AudioFormat.Builder()
                    .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                    .setSampleRate(AudioConfig.SAMPLE_RATE)
                    .setChannelMask(AudioFormat.CHANNEL_OUT_MONO)
                    .build()
            )
            .setTransferMode(AudioTrack.MODE_STREAM)
            .setBufferSizeInBytes(minBuffer * 4)
            .build()

        audioTrack?.setVolume(1.0f)
        audioTrack?.play()
        startPlaybackWriter()
    }

    fun playIncoming(data: ByteArray) {
        if (audioTrack == null) return
        if (data.isEmpty()) return
        enqueuePlaybackPcm(data)
    }

    /** Pitido local tipo radio; no afecta PTT ni red. */
    fun playPttTone(tone: PttTone) {
        val track = audioTrack ?: return
        val now = System.currentTimeMillis()
        if (tone == PttTone.OPENING && now - lastToneAtMs < AudioConfig.TONE_MIN_INTERVAL_MS) {
            return
        }
        lastToneAtMs = now

        val pcm = when (tone) {
            PttTone.OPENING -> generateTone(
                AudioConfig.TONE_OPEN_HZ,
                AudioConfig.TONE_OPEN_MS,
            )
            PttTone.ROGER_RX -> generateTone(
                AudioConfig.TONE_ROGER_RX_HZ,
                AudioConfig.TONE_ROGER_RX_MS,
            )
            PttTone.BUSY -> generateDoubleTone(
                AudioConfig.TONE_BUSY_HZ,
                AudioConfig.TONE_BUSY_MS,
            )
        }

        try {
            synchronized(playbackLock) {
                track.write(pcm, 0, pcm.size)
            }
        } catch (e: Exception) {
            Log.e(TAG, "Error reproduciendo tono PTT", e)
        }
    }

    fun startRecording() {
        if (recordJob?.isActive == true) return

        val minBuffer = AudioRecord.getMinBufferSize(
            AudioConfig.SAMPLE_RATE,
            AudioFormat.CHANNEL_IN_MONO,
            AudioFormat.ENCODING_PCM_16BIT
        )

        audioRecord = AudioRecord(
            MediaRecorder.AudioSource.MIC,
            AudioConfig.SAMPLE_RATE,
            AudioFormat.CHANNEL_IN_MONO,
            AudioFormat.ENCODING_PCM_16BIT,
            maxOf(minBuffer, AudioConfig.FRAME_BYTES * 2)
        )

        if (audioRecord?.state != AudioRecord.STATE_INITIALIZED) {
            Log.e(TAG, "AudioRecord no inicializado")
            return
        }

        audioRecord?.startRecording()
        dsp.reset()

        recordJob = scope.launch(Dispatchers.IO) {
            val buffer = ByteArray(AudioConfig.FRAME_BYTES)
            while (isActive) {
                val read = audioRecord?.read(buffer, 0, buffer.size) ?: 0
                if (read > 0) {
                    val frame = if (read == buffer.size) buffer.copyOf() else buffer.copyOf(read)
                    onAudioFrame(dsp.applyCaptureAgc(frame))
                }
            }
        }
    }

    fun stopRecording() {
        recordJob?.cancel()
        recordJob = null
        try {
            audioRecord?.stop()
            audioRecord?.release()
        } catch (_: Exception) {
        }
        audioRecord = null
    }

    fun refreshVolume() {
        volumeController.reapplyMaxVolume()
    }

    fun setPlaybackGain(gain: Float) {
        playbackGain = gain.coerceIn(0.5f, 6.0f)
    }

    fun release() {
        stopRecording()
        stopPlaybackWriter()
        synchronized(playbackLock) {
            try {
                audioTrack?.stop()
                audioTrack?.release()
            } catch (_: Exception) {
            }
            audioTrack = null
        }
        volumeController.restore()
    }

    private fun startPlaybackWriter() {
        if (playbackWriterJob?.isActive == true) return
        playbackWriterJob = scope.launch(Dispatchers.IO) {
            var primed = false
            var emptyPolls = 0
            while (isActive && audioTrack != null) {
                if (!primed) {
                    if (bufferedMsLocked() < AudioConfig.JITTER_BUFFER_MIN_MS) {
                        delay(POLL_DELAY_MS)
                        continue
                    }
                    primed = true
                    emptyPolls = 0
                }

                val chunk = dequeuePlaybackPcm()
                if (chunk == null) {
                    emptyPolls++
                    // Solo re-primar tras una pausa larga (fin de transmision / nueva rafaga),
                    // no por micro-huecos entre paquetes: eso causaba silencios y bajaba el volumen.
                    if (emptyPolls >= REPRIME_AFTER_EMPTY_POLLS) {
                        primed = false
                    }
                    delay(POLL_DELAY_MS)
                    continue
                }
                emptyPolls = 0

                val track = audioTrack ?: break
                try {
                    val boosted = dsp.applyGainLimited(chunk, playbackGain)
                    synchronized(playbackLock) {
                        track.write(boosted, 0, boosted.size)
                    }
                } catch (e: Exception) {
                    Log.e(TAG, "Error reproduciendo audio", e)
                }
            }
        }
    }

    private fun stopPlaybackWriter() {
        playbackWriterJob?.cancel()
        playbackWriterJob = null
        synchronized(queueLock) {
            playbackQueue.clear()
        }
    }

    private fun enqueuePlaybackPcm(pcm: ByteArray) {
        synchronized(queueLock) {
            playbackQueue.addLast(pcm)
            trimPlaybackQueueLocked()
        }
    }

    private fun dequeuePlaybackPcm(): ByteArray? {
        synchronized(queueLock) {
            return playbackQueue.removeFirstOrNull()
        }
    }

    private fun bufferedMsLocked(): Int {
        var bytes = 0
        for (chunk in playbackQueue) {
            bytes += chunk.size
        }
        return pcmBytesToMs(bytes)
    }

    private fun trimPlaybackQueueLocked() {
        while (bufferedMsLocked() > AudioConfig.JITTER_BUFFER_MAX_MS && playbackQueue.isNotEmpty()) {
            playbackQueue.removeFirst()
        }
    }

    private fun pcmBytesToMs(bytes: Int): Int {
        if (bytes <= 0) return 0
        val samples = bytes / 2
        return samples * 1000 / AudioConfig.SAMPLE_RATE
    }

    private fun generateTone(freqHz: Int, durationMs: Int): ByteArray {
        val sampleCount = AudioConfig.SAMPLE_RATE * durationMs / 1000
        val bytes = ByteArray(sampleCount * 2)
        val amplitude = AudioConfig.TONE_AMPLITUDE
        var i = 0
        while (i < sampleCount) {
            val envelope = toneEnvelope(i, sampleCount)
            val t = i.toDouble() / AudioConfig.SAMPLE_RATE
            val sample = (sin(2.0 * PI * freqHz * t) * 32767.0 * amplitude * envelope)
                .toInt()
                .coerceIn(-32768, 32767)
            bytes[i * 2] = (sample and 0xFF).toByte()
            bytes[i * 2 + 1] = ((sample shr 8) and 0xFF).toByte()
            i++
        }
        return bytes
    }

    private fun generateDoubleTone(freqHz: Int, beepMs: Int): ByteArray {
        val gapMs = 55
        val first = generateTone(freqHz, beepMs)
        val gapSamples = AudioConfig.SAMPLE_RATE * gapMs / 1000
        val gap = ByteArray(gapSamples * 2)
        val second = generateTone(freqHz, beepMs)
        return first + gap + second
    }

    private fun toneEnvelope(sampleIndex: Int, totalSamples: Int): Double {
        if (totalSamples <= 0) return 0.0
        val fadeSamples = (AudioConfig.SAMPLE_RATE * 0.008).toInt().coerceAtLeast(1)
        return when {
            sampleIndex < fadeSamples -> sampleIndex.toDouble() / fadeSamples
            sampleIndex > totalSamples - fadeSamples -> (totalSamples - sampleIndex).toDouble() / fadeSamples
            else -> 1.0
        }
    }

    companion object {
        private const val TAG = "AudioEngine"
        private const val POLL_DELAY_MS = 5L

        /** Sondeos vacios consecutivos antes de volver a exigir colchon de jitter. */
        private const val REPRIME_AFTER_EMPTY_POLLS = AudioConfig.JITTER_REPRIME_AFTER_MS / 5
    }
}
