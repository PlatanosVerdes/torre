package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/PlatanosVerdes/gonana/botstats"
	"github.com/PlatanosVerdes/torre/internal/adsbdb"
	"github.com/PlatanosVerdes/torre/internal/aena"
	"github.com/PlatanosVerdes/torre/internal/bot"
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
		tg        = telegram.New(env("TORRE_TELEGRAM_TOKEN", ""))
		owner     = env("TORRE_OWNER_CHAT", "")
		publicURL = env("TORRE_PUBLIC_URL", "")
		ipHeader  = env("TORRE_CLIENT_IP_HEADER", "")
		botName   = env("TORRE_BOT_NAME", "")
		// Only for a bot torre has to itself: a shared bot already has its one reader of updates.
		readBot   = env("TORRE_BOT_UPDATES", "") == "1"
		aenaURL   = env("TORRE_AENA_URL", aena.DefaultBaseURL)
		adsbdbURL = env("TORRE_ADSBDB_URL", adsbdb.DefaultBaseURL)
		logAsJSON = env("TORRE_LOG_JSON", "") == "1"
		// Lists names: never publish this address.
		statsAddr = env("TORRE_STATS_ADDR", "")
		collect   = strings.Fields(strings.ToUpper(strings.ReplaceAll(env("TORRE_COLLECT", ""), ",", " ")))
		sample    = number("TORRE_SAMPLE", 0)
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
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}

	client := aena.New(aenaURL, ttl)
	var notifier watch.Notifier = tg
	if sample > 0 {
		notifier = &watch.SampleNotifier{Next: tg, Path: filepath.Join(dataDir, "sample.jsonl"), Now: time.Now}
	}
	watcher, err := watch.New(dataDir, client, notifier, client.Zone, log)
	if err != nil {
		return err
	}
	watcher.BaseURL = publicURL
	watcher.Spanish = func(code string) bool {
		list, err := client.Airports(context.Background())
		if err != nil {
			return false
		}
		for _, a := range list {
			if a.IATA == code {
				return true
			}
		}
		return false
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	routes := adsbdb.New(adsbdbURL)
	ownBot := readBot && tg.Enabled() && publicURL != ""
	watcher.StopButton = ownBot
	go watcher.Run(ctx, every)
	if len(collect) > 0 {
		h := watch.NewHistory(filepath.Join(dataDir, "collect.jsonl"))
		go watch.Collect(ctx, client, collect, h, client.Zone, every, log)
		if sample > 0 {
			go watcher.Sample(ctx, collect, sample, every)
		}
	}
	if ownBot {
		b := &bot.Bot{TG: tg, Watch: watcher, BaseURL: publicURL, Version: buildVersion, Log: log,
			Routes: routes, Boards: client, Spanish: watcher.Spanish, Zone: client.Zone}
		if statsAddr != "" {
			rec, err := botstats.Open(filepath.Join(dataDir, "bot-stats.json"))
			if err != nil {
				return err
			}
			b.Stats = rec
			go rec.Loop(ctx)
			go serveStats(statsAddr, rec, botName, watcher, log)
		}
		go b.Run(ctx)
	}

	srv := &http.Server{
		Addr: ":" + strconv.Itoa(port),
		Handler: (&server.Server{
			Board: client, Routes: routes, Watch: watcher, Bot: tg, Version: buildVersion, Log: log,
			OwnerChat: owner, ClientIPHeader: ipHeader, BotName: botName,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// A two-week board can take AENA a minute and a half.
		WriteTimeout:   100 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()

	log.Info("torre listening", "version", buildVersion, "port", port,
		"telegram", tg.Enabled(), "bot_updates", readBot, "owner", owner != "", "public_url", publicURL, "collect", collect, "sample", sample)
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

func serveStats(addr string, rec *botstats.Recorder, name string, w *watch.Watcher, log *slog.Logger) {
	about := func() botstats.About {
		var flights []watch.Tracked
		chats := map[string]bool{}
		for _, t := range w.List("") {
			if !watch.Sampled(t) {
				flights = append(flights, t)
				chats[t.Chat] = true
			}
		}
		return botstats.About{Platform: "telegram", Bot: name, Name: "Torre", Extra: []botstats.Extra{
			{Label: "Flights followed", Value: strconv.Itoa(len(flights))},
			{Label: "Chats following one", Value: strconv.Itoa(len(chats))},
		}}
	}
	srv := &http.Server{Addr: addr, Handler: rec.Handler(about), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Warn("bot stats stopped", "err", err)
	}
}
