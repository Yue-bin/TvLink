// TvLink balances Tavily API keys for REST and MCP clients.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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

// version is overridden by release builds with -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	configPath, showVersion, err := parseArguments(os.Args[1:])
	if err != nil {
		slog.Error("parse command-line arguments", "error", err)
		os.Exit(2)
	}
	if showVersion {
		writeVersion(os.Stdout)
		return
	}
	settings, err := config.Load(configPath)
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
	if settings.GroupingEnabled() {
		location, _ := time.LoadLocation(settings.GroupRotationTimezone)
		if err := keyPool.ConfigureGroups(pool.GroupConfig{Size: settings.KeyGroupSize, UsageLimit: settings.GroupUsageLimit, Location: location}); err != nil {
			slog.Error("configure key groups", "error", err)
			os.Exit(1)
		}
		if err := keyPool.RebuildGroups(time.Now()); err != nil {
			slog.Error("build key groups", "error", err)
			os.Exit(1)
		}
	}
	go refreshLoop(ctx, usageClient, keys, settings.UsageRefreshInterval)

	selector := pool.NewCoordinator(keyPool, usageClient.RefreshAll)
	rest := proxy.NewWithCoordinator(settings.TvLinkAPIKey, "https://api.tavily.com", &http.Client{Transport: http.DefaultTransport}, keyPool, selector, usageClient, int64(settings.RequestBodyLimit), settings.ResearchMappingTTL)
	mux := http.NewServeMux()
	mux.Handle("/", monitor.New(keyPool))
	mux.Handle("/mcp", mcp.New(settings.TvLinkAPIKey, version, rest))
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

func parseArguments(args []string) (string, bool, error) {
	flags := flag.NewFlagSet("tvlink", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "tvlink.toml", "TOML configuration file")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(args); err != nil {
		return "", false, err
	}
	return *configPath, *showVersion, nil
}

func writeVersion(writer io.Writer) {
	_, _ = fmt.Fprintf(writer, "TvLink %s\n", version)
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
