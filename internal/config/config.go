package config

import (
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

// Load reads environment variables and returns a Config.
// devMode is passed in from main so that flag parsing stays in main.
func Load(devMode bool) *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("[CONFIG] No .env file found, relying on system env vars")
	}

	cfg := &Config{
		IsDev:              devMode,
		VPSIP:              getEnv("VPS_IP", "127.0.0.1"),
		VPSPort:            getEnvAsInt("VPS_PORT", 7000),
		AuthToken:          getEnv("AUTH_TOKEN", "default-secret"),
		Domain:             getEnv("DOMAIN", "localhost"),
		BackendURL:         getEnv("BACKEND_URL", ""),
		PprofPort:          getEnvAsInt("PPROF_PORT", 6060),
		TailScaleClientId:  getEnv("TAILSCALE_CLIENT_ID", ""),
		TailScaleAuthToken: getEnv("TAILSCALE_AUTH_TOKEN", ""),
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

// EffectiveBackendURL returns the configured URL or the default dev URL.
func (c *Config) EffectiveBackendURL() string {
	if c.BackendURL != "" {
		return c.BackendURL
	}
	return "https://dev.api.strct.org"
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
		log.Printf("[CONFIG] Invalid integer for %s, using default %d", key, fallback)
		return fallback
	}
	return val
}

// --- Wire provider types (used when Wire is wired up) ---

type BackendURL string
type DataDir string

func ProvideBackendURL(cfg *Config) BackendURL {
	return BackendURL(cfg.EffectiveBackendURL())
}

func ProvideDataDir(cfg *Config) DataDir {
	return DataDir(cfg.DataDir)
}