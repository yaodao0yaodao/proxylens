//go:build !windows

package main

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/yaodao0yaodao/proxylens/internal/config"
)

func runPlatform(_ context.Context, _ context.CancelFunc, server *http.Server, _ config.Config, _ *slog.Logger) error {
	return server.ListenAndServe()
}

func runRestartHelper() bool { return false }
