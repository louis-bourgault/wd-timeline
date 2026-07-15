package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

type pageviewsResponse struct {
	Items []struct {
		Views int `json:"views"`
	} `json:"items"`
}

func main() {
	connStr := flag.String("conn", "postgres://louis:password@localhost:5432/wd_timeline?sslmode=disable", "Database connection string")
	days := flag.Int("days", 60, "Number of days of pageviews to fetch")
	workers := flag.Int("workers", 75, "Number of concurrent API workers")
	flag.Parse()

	ctx := context.Background()

	conn, err := pgx.Connect(ctx, *connStr)
	if err != nil {
		log.Fatalf("Failed to connect to DB: %v", err)
	}
	defer conn.Close(ctx)

	fmt.Println("Querying database for unique Wikipedia articles...")
	rows, err := conn.Query(ctx, "SELECT DISTINCT wiki_url FROM events WHERE wiki_url IS NOT NULL AND wiki_url != ''")
	if err != nil {
		log.Fatalf("Failed to query wiki URLs: %v", err)
	}
	var wikiURLs []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err == nil {
			wikiURLs = append(wikiURLs, u)
		}
	}
	rows.Close()
	fmt.Printf("Found %d unique articles. Starting pageviews fetch (%d workers, %d-day window)...\n", len(wikiURLs), *workers, *days)

	end := time.Now().UTC()
	start := end.AddDate(0, 0, -*days)
	startStr := start.Format("20060102") + "00"
	endStr := end.Format("20060102") + "00"

	transport := &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 200,
		IdleConnTimeout:     30 * time.Second,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	type job struct {
		wikiURL string
		title   string
	}
	type result struct {
		wikiURL string
		views   int
	}

	jobs := make(chan job, 1000)
	results := make(chan result, 1000)

	limiter := time.NewTicker(10 * time.Millisecond)
	defer limiter.Stop()

	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				<-limiter.C
				views := fetchPageviews(client, j.title, startStr, endStr)
				results <- result{wikiURL: j.wikiURL, views: views}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	go func() {
		for _, u := range wikiURLs {
			title := extractTitle(u)
			if title != "" {
				jobs <- job{wikiURL: u, title: title}
			}
		}
		close(jobs)
	}()

	viewMap := make(map[string]int, len(wikiURLs))
	var processed int64
	startTime := time.Now()
	for r := range results {
		viewMap[r.wikiURL] = r.views
		n := atomic.AddInt64(&processed, 1)
		if n%10000 == 0 {
			elapsed := time.Since(startTime).Seconds()
			rate := float64(n) / elapsed
			remaining := float64(len(wikiURLs)-int(n)) / rate
			fmt.Printf("  %d/%d articles (%.0f/s, ~%.0f min remaining)\n", n, len(wikiURLs), rate, remaining/60)
		}
	}

	fmt.Printf("Fetched pageviews for %d articles in %v\n", len(viewMap), time.Since(startTime))

	type stat struct {
		wikiURL string
		views   int
	}
	stats := make([]stat, 0, len(viewMap))
	maxViews := 0
	for u, v := range viewMap {
		stats = append(stats, stat{u, v})
		if v > maxViews {
			maxViews = v
		}
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].views > stats[j].views })

	if maxViews == 0 {
		maxViews = 1
	}
	logMax := math.Log(float64(maxViews) + 1)

	type update struct {
		wikiURL      string
		importance   float32
		viewPriority int32
	}
	updates := make([]update, len(stats))
	priorityCounts := [5]int{}
	for i, s := range stats {
		imp := float32(0)
		if s.views > 0 {
			imp = float32(math.Log(float64(s.views)+1) / logMax)
		}
		percentile := float64(i) / float64(len(stats))
		prio := int32(1)
		switch {
		case percentile < 0.01:
			prio = 5
		case percentile < 0.05:
			prio = 4
		case percentile < 0.20:
			prio = 3
		case percentile < 0.50:
			prio = 2
		}
		updates[i] = update{s.wikiURL, imp, prio}
		priorityCounts[prio-1]++
	}

	fmt.Println("\nView priority distribution:")
	for p := 5; p >= 1; p-- {
		fmt.Printf("  Priority %d: %d articles\n", p, priorityCounts[p-1])
	}

	fmt.Println("\nUpdating database...")
	const batchSize = 5000
	for i := 0; i < len(updates); i += batchSize {
		end := i + batchSize
		if end > len(updates) {
			end = len(updates)
		}
		batch := updates[i:end]
		urls := make([]string, len(batch))
		imps := make([]float32, len(batch))
		prios := make([]int32, len(batch))
		for j, u := range batch {
			urls[j] = u.wikiURL
			imps[j] = u.importance
			prios[j] = u.viewPriority
		}
		_, err := conn.Exec(ctx, `
			UPDATE events e
			SET view_priority = d.priority, importance = d.importance
			FROM unnest($1::text[], $2::real[], $3::int[]) AS d(url, importance, priority)
			WHERE e.wiki_url = d.url
		`, urls, imps, prios)
		if err != nil {
			log.Printf("Error updating batch %d: %v", i/batchSize, err)
		}
		if (i/batchSize)%20 == 0 {
			fmt.Printf("  Updated %d/%d articles\n", end, len(updates))
		}
	}

	fmt.Println("Done! Database updated with pageview-based importance scores.")
}

func extractTitle(wikiURL string) string {
	const prefix = "https://en.wikipedia.org/wiki/"
	if !strings.HasPrefix(wikiURL, prefix) {
		return ""
	}
	title := strings.TrimPrefix(wikiURL, prefix)
	unescaped, err := url.PathUnescape(title)
	if err != nil {
		return title
	}
	return unescaped
}

func fetchPageviews(client *http.Client, title, start, end string) int {
	apiURL := fmt.Sprintf("https://wikimedia.org/api/rest_v1/metrics/pageviews/per-article/en.wikipedia/all-access/user/%s/daily/%s/%s",
		url.PathEscape(title), start, end)

	for retry := 0; retry < 3; retry++ {
		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			return 0
		}
		req.Header.Set("User-Agent", "wikidata-timeline/1.0 (https://github.com/louis-bourgault/wikidata-timeline)")

		resp, err := client.Do(req)
		if err != nil {
			if retry < 2 {
				time.Sleep(time.Duration(retry+1) * time.Second)
			}
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode == 200:
			var pv pageviewsResponse
			if err := json.Unmarshal(body, &pv); err != nil {
				return 0
			}
			total := 0
			for _, item := range pv.Items {
				total += item.Views
			}
			return total
		case resp.StatusCode == 429:
			if retry < 2 {
				time.Sleep(time.Duration(retry+1) * 2 * time.Second)
			}
			continue
		default:
			return 0
		}
	}
	return 0
}
