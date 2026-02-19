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
	DeviceID           string
	Domain             string
	VPSIP              string
	AuthToken          string
	DataDir            string
	BackendURL         string
	TailScaleClientId  string
	TailScaleAuthToken string
	VPSPort            int
	PprofPort          int
	IsDev              bool
}

func Load() *Config {
	devMode := flag.Bool("dev", false, "Run in development mode (Mock hardware)")
	flag.Parse()

	if err := godotenv.Load(); err != nil {
		log.Println("[CONFIG] No .env file found, relying on system env vars")
	}

	cfg := &Config{
		IsDev:              *devMode,
		VPSIP:              getEnv("VPS_IP", "127.0.0.1"),
		VPSPort:            getEnvAsInt("VPS_PORT", 7000),
		AuthToken:          getEnv("AUTH_TOKEN", "default-secret"),
		Domain:             getEnv("DOMAIN", "localhost"),
		PprofPort:          getEnvAsInt("PPROF_PORT", 6060),
		TailScaleClientId:  getEnv("TAILSCALE_CLIENT_ID", "no_ts_client_id"),
		TailScaleAuthToken: getEnv("TAILSCALE_AUTH_TOKEN", "no_auth_token"),
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

type BackendURL string
type DataDir string

func ProvideBackendURL(cfg *Config) BackendURL {
	if cfg.BackendURL != "" {
		return BackendURL(cfg.BackendURL)
	}
	return "https://dev.api.strct.org"
}

func ProvideDataDir(cfg *Config) DataDir {
	return DataDir(cfg.DataDir)
}
