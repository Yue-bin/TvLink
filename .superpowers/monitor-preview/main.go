package main

import (
	"log"
	"net/http"
	"time"

	"tvlink/internal/monitor"
	"tvlink/internal/pool"
)

func main() {
	now := time.Now()
	keyPool := pool.New([]pool.Key{
		{Name: "primary-01"},
		{Name: "primary-shanghai-long-name"},
		{Name: "backup-cn"},
		{Name: "primary-04"},
		{Name: "primary-05"},
		{Name: "backup-us"},
	}, 7)
	keyPool.UpdateUsage("primary-01", pool.Usage{Limit: 500, Used: 210}, now.Add(-12*time.Second))
	keyPool.UpdateUsage("primary-shanghai-long-name", pool.Usage{Limit: 500, Used: 330}, now.Add(-18*time.Second))
	keyPool.UpdateUsage("backup-cn", pool.Usage{Limit: 500, Used: 200}, now.Add(-23*time.Second))
	keyPool.UpdateUsage("primary-04", pool.Usage{Limit: 500, Used: 180}, now.Add(-9*time.Second))
	keyPool.UpdateUsage("primary-05", pool.Usage{Limit: 500, Used: 260}, now.Add(-15*time.Second))
	keyPool.UpdateUsage("backup-us", pool.Usage{Limit: 500, Used: 290}, now.Add(-21*time.Second))
	if err := keyPool.ConfigureGroups(pool.GroupConfig{Size: 2, UsageLimit: 100, Location: time.Local}); err != nil {
		log.Fatal(err)
	}
	if err := keyPool.RebuildGroups(now); err != nil {
		log.Fatal(err)
	}
	if _, err := keyPool.Select(now, 18); err != nil {
		log.Fatal(err)
	}
	if lease, err := keyPool.Select(now, 37); err != nil {
		log.Fatal(err)
	} else {
		keyPool.Resolve(lease, http.StatusTooManyRequests, 42*time.Second, now)
	}
	log.Fatal(http.ListenAndServe("127.0.0.1:18081", monitor.New(keyPool)))
}
