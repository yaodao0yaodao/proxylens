//go:build !windows

package config

func defaultDataDir() string      { return "/var/lib/proxylens" }
func defaultListen(string) string { return "0.0.0.0:9099" }
func defaultSingBoxPath() string  { return "/usr/lib/proxylens/sing-box" }
