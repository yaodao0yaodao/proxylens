package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func defaultDataDir() string {
	if base := os.Getenv("LOCALAPPDATA"); base != "" {
		return filepath.Join(base, "ProxyLens")
	}
	dir, err := os.UserConfigDir()
	if err == nil && dir != "" {
		return filepath.Join(dir, "ProxyLens")
	}
	return filepath.Join(".", "ProxyLens-data")
}

func defaultListen(dataDir string) string {
	port := 9099
	if raw, err := os.ReadFile(filepath.Join(dataDir, "web-port")); err == nil {
		if value, parseErr := strconv.Atoi(strings.TrimSpace(string(raw))); parseErr == nil && value > 0 && value <= 65535 {
			port = value
		}
	}
	return "127.0.0.1:" + strconv.Itoa(port)
}

func defaultSingBoxPath() string {
	if executable, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(executable), "sing-box.exe")
	}
	return "sing-box.exe"
}
