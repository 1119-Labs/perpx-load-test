package loadtest

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// startStandaloneTUI starts a lightweight full-screen terminal UI that updates once per second.
// It is intentionally dependency-free (ANSI escape codes only) so it works anywhere SSH works.
//
// NOTE: This is designed for standalone mode. It reads stats from the TransactorGroup, so it
// doesn't need extra plumbing from transactors.
func startStandaloneTUI(cfg *Config, tg *TransactorGroup) func() {
	stopc := make(chan struct{})
	stopped := make(chan struct{})

	// UI state for instantaneous rates.
	var (
		lastTime      = time.Now()
		lastTotalTxs  = 0
		lastTotalByte = int64(0)
		lastByEP      = map[string]int{}
		lastByEPBytes = map[string]int64{}
	)

	hideCursor := func() { fmt.Fprint(os.Stdout, "\033[?25l") }
	showCursor := func() { fmt.Fprint(os.Stdout, "\033[?25h") }
	clearScreen := func() { fmt.Fprint(os.Stdout, "\033[H\033[2J") }

	hideCursor()
	clearScreen()

	go func() {
		defer close(stopped)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				now := time.Now()
				dt := now.Sub(lastTime).Seconds()
				if dt <= 0 {
					dt = 1
				}

				// Snapshot group stats.
				type epAgg struct {
					tx    int
					bytes int64
				}
				byEP := map[string]*epAgg{}

				tg.statsMtx.RLock()
				startTime := tg.startTime
				// Copy current txCounts/txBytes and map to endpoints.
				for id, txc := range tg.txCounts {
					ep := "unknown"
					if id >= 0 && id < len(tg.transactors) {
						ep = tg.transactors[id].remoteAddr
					}
					agg := byEP[ep]
					if agg == nil {
						agg = &epAgg{}
						byEP[ep] = agg
					}
					agg.tx += txc
					agg.bytes += tg.txBytes[id]
				}
				tg.statsMtx.RUnlock()

				totalTxs := 0
				totalBytes := int64(0)
				for _, agg := range byEP {
					totalTxs += agg.tx
					totalBytes += agg.bytes
				}

				// Compute instantaneous rates (delta since last tick).
				instTxRate := float64(totalTxs-lastTotalTxs) / dt
				instByteRate := float64(totalBytes-lastTotalByte) / dt

				// Render.
				clearScreen()
				elapsed := 0 * time.Second
				if !startTime.IsZero() {
					elapsed = time.Since(startTime)
				}

				fmt.Fprintf(os.Stdout, "PerpX Load Test (TUI)\n")
				fmt.Fprintf(os.Stdout, "elapsed: %s / %ds   connections: %d   send_period: %ds   rate: %d tx/s/conn\n",
					elapsed.Truncate(time.Second).String(),
					cfg.Time,
					cfg.Connections*len(cfg.Endpoints),
					cfg.SendPeriod,
					cfg.Rate,
				)
				fmt.Fprintf(os.Stdout, "total: %d tx   inst: %.0f tx/s   inst data: %.1f KiB/s\n",
					totalTxs, instTxRate, instByteRate/1024.0,
				)
				fmt.Fprintf(os.Stdout, "endpoints: %s\n", strings.Join(cfg.Endpoints, ", "))
				fmt.Fprintf(os.Stdout, "\n")

				// Table header.
				fmt.Fprintf(os.Stdout, "%-42s  %12s  %10s  %12s\n", "endpoint", "txs", "tx/s", "KiB/s")
				fmt.Fprintf(os.Stdout, "%s\n", strings.Repeat("-", 82))

				// Sorted endpoints for stable display.
				eps := make([]string, 0, len(byEP))
				for ep := range byEP {
					eps = append(eps, ep)
				}
				sort.Strings(eps)

				for _, ep := range eps {
					agg := byEP[ep]
					prevTx := lastByEP[ep]
					prevB := lastByEPBytes[ep]
					epTxRate := float64(agg.tx-prevTx) / dt
					epBRate := float64(agg.bytes-prevB) / dt
					fmt.Fprintf(os.Stdout, "%-42s  %12d  %10.0f  %12.1f\n",
						trimForTable(ep, 42),
						agg.tx,
						epTxRate,
						epBRate/1024.0,
					)
				}

				fmt.Fprintf(os.Stdout, "\nPress Ctrl+C to stop.\n")
				_ = os.Stdout.Sync()

				// Update last snapshot.
				lastTime = now
				lastTotalTxs = totalTxs
				lastTotalByte = totalBytes
				lastByEP = map[string]int{}
				lastByEPBytes = map[string]int64{}
				for ep, agg := range byEP {
					lastByEP[ep] = agg.tx
					lastByEPBytes[ep] = agg.bytes
				}

			case <-stopc:
				return
			}
		}
	}()

	return func() {
		select {
		case <-stopc:
			// already stopped
		default:
			close(stopc)
		}
		<-stopped
		// Restore terminal state.
		clearScreen()
		showCursor()
	}
}

func trimForTable(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}




