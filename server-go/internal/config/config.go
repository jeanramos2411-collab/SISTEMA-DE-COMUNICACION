package config

import (
	"os"
)

const (
	Host            = "0.0.0.0"
	Port            = 8765
	AdminPort       = 8766
	DefaultGain     = 3.0
	SampleRate     = 16000
	Channels       = 1
	Bits           = 16
	SaveDebounceMs = 3000
	AudioFormat    = "pcm"
	DataDir        = "data"
	ConfigFile     = "data/config.json"
)

func GetEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
