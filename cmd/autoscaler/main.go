// Copyright (c) 2026 VGS https://vgst.net
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"proxmox-autoscaler/internal/config"
	"proxmox-autoscaler/internal/db"
	"proxmox-autoscaler/internal/monitor"
	"proxmox-autoscaler/internal/notifier"
	"proxmox-autoscaler/internal/proxmox"
)

var version = "1.0.10"

func main() {
	configPath := flag.String("config", "/etc/proxmox-autoscaler/autoscaler.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	// Load config first (before logger so we can configure log output).
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: load config: %v\n", err)
		os.Exit(1)
	}

	// Set up logger.
	logger, logFile, err := buildLogger(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: build logger: %v\n", err)
		os.Exit(1)
	}
	if logFile != nil {
		defer logFile.Close()
	}

	// Determine hostname for email subjects.
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	// Open database.
	database, err := db.Open(cfg.Storage.DBPath, logger)
	if err != nil {
		logger.Error("DB error", "operation", "Open", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// Create Proxmox client.
	client := proxmox.NewClient(
		cfg.Proxmox.Host,
		cfg.Proxmox.Node,
		cfg.Proxmox.TokenID,
		cfg.Proxmox.TokenSecret,
		cfg.Proxmox.InsecureTLS,
	)

	// Create notifiers.
	var backends []notifier.Notifier
	if cfg.Notifications.Email.Enabled {
		backends = append(backends, notifier.New(
			cfg.Notifications.Email.Enabled,
			cfg.Notifications.Email.MailBinary,
			cfg.Notifications.Email.To,
			cfg.Notifications.Email.Language,
			logger,
		))
	}
	if cfg.Notifications.Slack.Enabled {
		backends = append(backends, notifier.NewSlack(
			cfg.Notifications.Slack.Enabled,
			cfg.Notifications.Slack.Token,
			cfg.Notifications.Slack.Channel,
			logger,
		))
	}
	notif := notifier.NewMulti(backends...)

	// Create and start monitor.
	mon, err := monitor.New(cfg, client, database, notif, logger, hostname)
	if err != nil {
		logger.Error("failed to initialise monitor", "error", err)
		os.Exit(1)
	}

	logger.Info("service started",
		"version", version,
		"config_file", *configPath,
		"node", cfg.Proxmox.Node,
		"poll_interval", cfg.Monitor.PollInterval.String(),
	)

	mon.Start()

	// Block until signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	logger.Info("service stopping")

	// Stop monitor (cancels poll loop).
	mon.Stop()

	// Revert all active boosts.
	mon.RevertAllBoosts(context.Background())

	// Close DB.
	if err := database.Close(); err != nil {
		logger.Error("DB error", "operation", "Close", "error", err)
	}

	logger.Info("service stopped cleanly")
}

// buildLogger constructs an slog.Logger based on the logging config.
// It returns the logger and an optional file handle that must be closed.
func buildLogger(cfg *config.Config) (*slog.Logger, *os.File, error) {
	level := slog.LevelInfo
	switch cfg.Logging.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}

	// Primary output: stdout.
	var writers []io.Writer
	writers = append(writers, os.Stdout)

	var logFile *os.File
	if cfg.Logging.File != "" {
		f, err := os.OpenFile(cfg.Logging.File, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			// Non-fatal: warn on stderr and continue without file logging.
			fmt.Fprintf(os.Stderr, "warning: cannot open log file %q: %v\n", cfg.Logging.File, err)
		} else {
			logFile = f
			writers = append(writers, f)
		}
	}

	var w io.Writer
	if len(writers) == 1 {
		w = writers[0]
	} else {
		w = io.MultiWriter(writers...)
	}

	var handler slog.Handler
	if cfg.Logging.Format == "json" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	return slog.New(handler), logFile, nil
}
