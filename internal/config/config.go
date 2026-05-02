package config

import (
	"os"

	"github.com/joho/godotenv"
)

func Load() {
	_ = godotenv.Load()
}

func Get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func MustGet(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic("missing required env: " + key)
	}
	return v
}
