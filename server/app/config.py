HOST = "0.0.0.0"
PORT = 8765
ADMIN_PORT = 8766

# Valor inicial; el panel admin puede cambiarlo en vivo (server/data/config.json)
DEFAULT_PLAYBACK_GAIN = 3.0

AUDIO_SAMPLE_RATE = 16000
AUDIO_CHANNELS = 1
AUDIO_BITS = 16

# Guardado diferido de config.json (segundos)
SAVE_DEBOUNCE_SECONDS = 3.0

# Formato de audio en WebSocket (PCM 16-bit crudo; servidor solo reenvia)
AUDIO_FORMAT = "pcm"
