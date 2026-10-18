package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/PlatanosVerdes/torre/internal/aena"
	"github.com/PlatanosVerdes/torre/internal/server"
	"github.com/PlatanosVerdes/torre/internal/telegram"
	"github.com/PlatanosVerdes/torre/internal/watch"
)

var buildVersion = "local"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		port      = number("TORRE_PORT", 8000)
		dataDir   = env("TORRE_DATA_DIR", "./data")
		ttl       = duration("TORRE_CACHE", 30*time.Second)
		every     = duration("TORRE_CHECK_EVERY", 2*time.Minute)
		bot       = telegram.New(env("TORRE_TELEGRAM_TOKEN", ""), env("TORRE_TELEGRAM_CHAT", ""))
		aenaURL   = env("TORRE_AENA_URL", aena.DefaultBaseURL)
		logAsJSON = env("TORRE_LOG_JSON", "") == "1"
	)
	// Under ten seconds the cache stops protecting AENA from a page left open on several phones.
	if ttl < 10*time.Second {
		return fmt.Errorf("TORRE_CACHE (%s) is under 10s", ttl)
	}
	if every < 30*time.Second {
		return fmt.Errorf("TORRE_CHECK_EVERY (%s) is under 30s", every)
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if logAsJSON {
		log = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}

	client := aena.New(aenaURL, ttl)
	watcher, err := watch.New(dataDir, client, bot, client.Zone, log)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go watcher.Run(ctx, every)

	srv := &http.Server{
		Addr: ":" + strconv.Itoa(port),
		Handler: (&server.Server{
			Board: client, Watch: watcher, Version: buildVersion, Log: log, Notify: bot.Enabled(),
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Info("torre listening", "version", buildVersion, "port", port, "telegram", bot.Enabled())
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func number(key string, def int) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return def
	}
	return n
}

func duration(key string, def time.Duration) time.Duration {
	d, err := time.ParseDuration(os.Getenv(key))
	if err != nil {
		return def
	}
	return d
}
