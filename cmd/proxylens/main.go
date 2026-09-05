package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yaodao0yaodao/proxylens/internal/config"
	"github.com/yaodao0yaodao/proxylens/internal/logfile"
	"github.com/yaodao0yaodao/proxylens/internal/probe"
	"github.com/yaodao0yaodao/proxylens/internal/service"
	"github.com/yaodao0yaodao/proxylens/internal/store"
	webui "github.com/yaodao0yaodao/proxylens/internal/web"
)

var version = "dev"

func main() {
	if runRestartHelper() {
		return
	}
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-V" || os.Args[1] == "version") {
		fmt.Printf("proxylens %s\n", version)
		return
	}
	cfg := config.Load()
	if e := os.MkdirAll(cfg.DataDir, 0700); e != nil {
		panic(e)
	}
	file, e := logfile.Open(cfg.LogPath, cfg.MaxLogBytes)
	if e != nil {
		panic(e)
	}
	defer file.Close()
	log := slog.New(slog.NewJSONHandler(io.MultiWriter(os.Stderr, file), &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)
	st, e := store.Open(cfg.DBPath)
	if e != nil {
		log.Error("open database", "error", e)
		os.Exit(1)
	}
	defer st.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if os.Getenv("PROXYLENS_ADMIN_TOKEN") == "" {
		if saved, se := st.Setting(ctx, "admin_token"); se == nil && saved != "" {
			cfg.AdminToken = saved
		} else {
			_ = st.PutSetting(ctx, "admin_token", cfg.AdminToken)
		}
	}
	p := &probe.Engine{SingBoxPath: cfg.SingBoxPath, DataDir: cfg.DataDir, PortStart: cfg.ProbePortStart, Log: log}
	svc := service.New(st, p, cfg.Schedule, log)
	svc.PublicBaseURL = cfg.PublicBaseURL
	server := &webui.Server{Service: svc, AdminToken: cfg.AdminToken, LogPath: cfg.LogPath, PublicBaseURL: cfg.PublicBaseURL, Version: version, Log: log}
	httpServer := &http.Server{Addr: cfg.Listen, Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 5 * time.Minute, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 32 << 10}
	// Existing artifacts are usable immediately. Recalculate and regenerate in
	// the background so large databases never delay the Web/LuCI entry point.
	go svc.RegenerateStored(ctx)
	go svc.Scheduler(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 15*time.Second)
		defer c()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	go maintenance(ctx, st, cfg.MaxDatabaseBytes, log)
	log.Info("ProxyLens started", "version", version, "listen", cfg.Listen, "data_dir", cfg.DataDir, "admin_token_generated", os.Getenv("PROXYLENS_ADMIN_TOKEN") == "")
	if e = runPlatform(ctx, cancel, httpServer, cfg, log); e != nil && !errors.Is(e, http.ErrServerClosed) {
		log.Error("HTTP server", "error", e)
		os.Exit(1)
	}
	log.Info("ProxyLens stopped")
}

func maintenance(ctx context.Context, st *store.Store, maxBytes int64, log *slog.Logger) {
	if e := st.Compact(ctx, maxBytes); e != nil {
		log.Warn("database compact", "error", e)
	}
	tick := time.NewTicker(24 * time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if e := st.Compact(ctx, maxBytes); e != nil {
				log.Warn("database compact", "error", e)
			}
		}
	}
}
