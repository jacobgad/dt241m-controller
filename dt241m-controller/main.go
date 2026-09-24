// Command dt241m-controller is the Home Assistant add-on binary: it bridges PWAY DT241M
// HDMI-over-IP units to MQTT so they appear as native Home Assistant devices.
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

const (
	supportURL      = "https://github.com/jacobgad/dt241m-controller"
	shutdownTimeout = 10 * time.Second
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg, err := config.Load(ctx)
	if err != nil {
		newLogger(slog.LevelInfo).Error("config_invalid", "detail", err.Error())
		return err
	}
	log := newLogger(cfg.Options.LogLevel)

	db, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		log.Error("database_open_failed", "path", cfg.DatabasePath, "error", err.Error())
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Warn("database_close_failed", "error", err.Error())
		}
	}()
	log.Info("database_ready", "path", cfg.DatabasePath)

	conn, err := mqtt.Connect(ctx, mqtt.PahoOptions{
		Settings: cfg.MQTT,
		ClientID: fmt.Sprintf("dt241m-controller-%d", os.Getpid()),
		Will:     mqtt.Will{Topic: mqtt.ControllerAvailability, Payload: mqtt.PayloadOffline},
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
		shutdown(ctrl)
		return err
	}

	<-ctx.Done()
	log.Info("shutdown_requested")
	shutdown(ctrl)
	return nil
}

func shutdown(ctrl *controller.Controller) {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	ctrl.Stop(ctx)
}

func newLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
