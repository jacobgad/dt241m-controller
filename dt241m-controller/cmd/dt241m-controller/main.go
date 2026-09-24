package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jacobgad/dt241m-controller/internal/config"
	"github.com/jacobgad/dt241m-controller/internal/controller"
	"github.com/jacobgad/dt241m-controller/internal/dt241m"
	"github.com/jacobgad/dt241m-controller/internal/mqtt"
	"github.com/jacobgad/dt241m-controller/internal/store"
)

var version = "dev"

const supportURL = "https://github.com/jacobgad/dt241m-controller"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	bootLog := newLogger("info")
	cfg, err := config.Load(ctx)
	if err != nil {
		bootLog.Error("config_invalid", "detail", err.Error())
		return err
	}
	log := newLogger(cfg.Options.LogLevel)

	db, err := store.Open(cfg.DatabasePath)
	if err != nil {
		log.Error("database_open_failed", "path", cfg.DatabasePath, "error", err.Error())
		return err
	}
	defer db.Close()
	log.Info("database_ready", "path", cfg.DatabasePath)

	conn, err := mqtt.Connect(ctx, mqtt.PahoOptions{
		Settings: cfg.MQTT,
		ClientID: fmt.Sprintf("dt241m-controller-%d", os.Getpid()),
		Will:     struct{ Topic, Payload string }{mqtt.ControllerAvailability, mqtt.PayloadOffline},
		Log:      log,
	})
	if err != nil {
		log.Error("mqtt_setup_failed", "error", err.Error())
		return err
	}

	ctrl := controller.New(controller.Deps{
		Client:  dt241m.NewHTTPClient(dt241m.Options{Timeout: cfg.Options.ProbeTimeout}),
		MQTT:    conn,
		Store:   db,
		Options: cfg.Options,
		Log:     log,
		Origin:  mqtt.Origin{Version: version, SupportURL: supportURL},
	})
	if err := ctrl.Start(ctx); err != nil {
		log.Error("startup_failed", "error", err.Error())
		return err
	}

	<-ctx.Done()
	log.Info("shutdown_requested")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctrl.Stop(shutdownCtx)
	return nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
