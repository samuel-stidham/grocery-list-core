package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Probe timing. The service manager reports a process the moment it exists,
// before it has opened its database or bound its port, so callers ask the
// daemon itself. Opening the database takes well under a second on a cold
// cache.
const (
	probeInterval = 200 * time.Millisecond
	probeRequest  = time.Second
)

// WaitFor asks url every 200 ms until it answers 200 or timeout passes.
// Anything else within the timeout is ErrNotActive, carrying the last answer
// or connection error. A url that cannot be requested at all is ErrNotActive
// at once, since waiting would not change it. A canceled ctx, an interrupt in
// practice, is ErrInterrupted, because the daemon may still be coming up.
func WaitFor(ctx context.Context, url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("%w: probe request for %s: %w", ErrNotActive, url, err)
	}

	client := &http.Client{Timeout: probeRequest}
	ticker := time.NewTicker(probeInterval)

	defer ticker.Stop()

	for {
		last := probeOnce(client, req)
		if last == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return fmt.Errorf("%w while waiting for %s", ErrInterrupted, url)
			}

			return fmt.Errorf("%w: no answer from %s within %s: %w", ErrNotActive, url, timeout, last)
		case <-ticker.C:
		}
	}
}

// probeOnce sends the request and reads the body out so the connection is
// reused. The request has no body, so it can be sent again.
func probeOnce(client *http.Client, req *http.Request) error {
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("probe: %s answered %d", req.URL, resp.StatusCode)
	}

	return nil
}
