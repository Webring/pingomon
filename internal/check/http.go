package check

import (
	"net/http"
	"time"
)

type PingResult struct {
	URL        string
	StatusCode int
	Duration   time.Duration
	Err        error
}

func HttpPing(url string) PingResult {
	start := time.Now()

	resp, err := http.Get(url)
	if err != nil {
		return PingResult{URL: url, Err: err}
	}
	defer resp.Body.Close()

	return PingResult{
		URL:        url,
		StatusCode: resp.StatusCode,
		Duration:   time.Since(start),
	}
}
