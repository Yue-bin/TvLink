// TvLink balances Tavily API keys for REST and MCP clients.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tvlink/internal/config"
	"tvlink/internal/mcp"
	"tvlink/internal/monitor"
	"tvlink/internal/pool"
	"tvlink/internal/proxy"
	"tvlink/internal/tavily"
)

func main() {
	configPath := flag.String("config", "tvlink.toml", "TOML configuration file")
	flag.Parse()
	settings, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load configuration", "error", err)
		os.Exit(1)
	}
	keys := make([]pool.Key, 0, len(settings.TavilyKeys))
	for _, key := range settings.TavilyKeys {
		keys = append(keys, pool.Key{Name: key.Name, APIKey: key.APIKey})
	}
	keyPool := pool.New(keys, time.Now().UnixNano())
	usageClient := tavily.NewClient("https://api.tavily.com", &http.Client{Timeout: 20 * time.Second}, keyPool, keys)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for _, key := range keys {
		if err := usageClient.RefreshUsage(ctx, key.Name); err != nil {
			slog.Warn("initial usage refresh failed", "key", key.Name, "error", err)
		}
	}
	go refreshLoop(ctx, usageClient, keys, settings.UsageRefreshInterval)

	rest := proxy.New(settings.TvLinkAPIKey, "https://api.tavily.com", &http.Client{Transport: http.DefaultTransport}, keyPool, int64(settings.RequestBodyLimit))
	mux := http.NewServeMux()
	mux.Handle("/", monitor.New(keyPool, settings.MonitorRefreshInterval))
	mux.Handle("/mcp", mcp.New(settings.TvLinkAPIKey, rest))
	mux.Handle("/search", rest)
	mux.Handle("/extract", rest)
	mux.Handle("/crawl", rest)
	mux.Handle("/map", rest)
	mux.Handle("/research", rest)
	mux.Handle("/research/", rest)
	server := &http.Server{Addr: settings.ListenAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("shutdown server", "error", err)
		}
	}()
	slog.Info("TvLink started", "addr", settings.ListenAddr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("serve", "error", err)
		os.Exit(1)
	}
}

func refreshLoop(ctx context.Context, client *tavily.Client, keys []pool.Key, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, key := range keys {
				if err := client.RefreshUsage(ctx, key.Name); err != nil {
					slog.Warn("usage refresh failed", "key", key.Name, "error", err)
				}
			}
		}
	}
}
