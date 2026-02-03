package config

import (
	"flag"
	"log"
	"os"
	"runtime"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	IsDev    bool
	DeviceID string
	Domain   string

	VPSIP     string
	VPSPort   int
	AuthToken string

	DataDir string
}

func Load() *Config {
	devMode := flag.Bool("dev", false, "Run in development mode (Mock hardware)")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		log.Println("[CONFIG] No .env file found, relying on system env vars")
	}

	cfg := &Config{
		IsDev:     *devMode,
		VPSIP:     getEnv("VPS_IP", "127.0.0.1"),
		VPSPort:   getEnvAsInt("VPS_PORT", 7000),
		AuthToken: getEnv("AUTH_TOKEN", "default-secret"),
		Domain:    getEnv("DOMAIN", "localhost"),
	}

	if cfg.IsArm64() {
		cfg.DataDir = "/mnt/data"
	} else {
		cfg.DataDir = "./data"
	}

	cfg.DeviceID = getOrGenerateDeviceID(cfg.IsDev)

	return cfg
}
func (c *Config) IsArm64() bool {
	return runtime.GOOS == "linux" && runtime.GOARCH == "arm64" && !c.IsDev

}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	strValue := getEnv(key, "")
	if strValue == "" {
		return fallback
	}
	val, err := strconv.Atoi(strValue)
	if err != nil {
		log.Printf("[CONFIG] Warning: Invalid integer for %s, using default: %d", key, fallback)
		return fallback
	}
	return val
}
