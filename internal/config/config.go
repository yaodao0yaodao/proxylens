package config

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/yaodao0yaodao/proxylens/internal/model"
)

type Config struct {
	Listen           string
	DataDir          string
	DBPath           string
	LogPath          string
	AdminToken       string
	PublicBaseURL    string
	SingBoxPath      string
	ProbePortStart   int
	MaxLogBytes      int64
	MaxDatabaseBytes int64
	Schedule         model.Schedule
}

func Load() Config {
	data := env("PROXYLENS_DATA_DIR", "/var/lib/proxylens")
	c := Config{
		Listen: env("PROXYLENS_LISTEN", "0.0.0.0:9099"), DataDir: data,
		DBPath: os.Getenv("PROXYLENS_DATABASE"), LogPath: filepath.Join(data, "proxylens.log"),
		AdminToken: os.Getenv("PROXYLENS_ADMIN_TOKEN"), PublicBaseURL: os.Getenv("PROXYLENS_PUBLIC_BASE_URL"),
		SingBoxPath: env("PROXYLENS_SING_BOX", "/usr/lib/proxylens/sing-box"), ProbePortStart: envInt("PROXYLENS_PROBE_PORT_START", 19000),
		MaxLogBytes: int64(envInt("PROXYLENS_MAX_LOG_MB", 8)) << 20, MaxDatabaseBytes: int64(envInt("PROXYLENS_MAX_DB_MB", 96)) << 20,
		Schedule: model.Schedule{DetectionInterval: time.Hour, RulesInterval: 24 * time.Hour},
	}
	flag.StringVar(&c.Listen, "listen", c.Listen, "management and subscription listen address")
	flag.StringVar(&c.DataDir, "data-dir", c.DataDir, "persistent data directory")
	flag.StringVar(&c.DBPath, "database", c.DBPath, "SQLite database path")
	flag.StringVar(&c.SingBoxPath, "sing-box", c.SingBoxPath, "sing-box executable used for exit probes")
	flag.Parse()
	if c.DBPath == "" {
		c.DBPath = filepath.Join(c.DataDir, "proxylens.db")
	}
	c.LogPath = filepath.Join(c.DataDir, "proxylens.log")
	if c.AdminToken == "" {
		c.AdminToken = randomToken(24)
	}
	return c
}

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func envInt(k string, fallback int) int {
	v, e := strconv.Atoi(os.Getenv(k))
	if e == nil && v > 0 {
		return v
	}
	return fallback
}
func randomToken(n int) string {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}
