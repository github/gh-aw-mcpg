package launcher

import (
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

const (
	// DefaultHealthCheckInterval is the recommended periodic health check interval (spec §8).
	DefaultHealthCheckInterval = 30 * time.Second

	// maxConsecutiveRestartFailures caps how many consecutive restart failures
	// are allowed before the monitor stops retrying a particular server.
	maxConsecutiveRestartFailures = 3
)

var logHealth = logger.New("launcher:health")

// HealthMonitor periodically checks backend server health and automatically
// restarts servers that are in an error state (MCP Gateway Specification §8).
type HealthMonitor struct {
	launcher *Launcher
	interval time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}

	// Track consecutive restart failures per server to avoid infinite retry loops.
	consecutiveFailures *util.FailureCounter[string]
}

// NewHealthMonitor creates a health monitor for the given launcher.
func NewHealthMonitor(l *Launcher, interval time.Duration) *HealthMonitor {
	interval = util.PositiveOrDefault(interval, DefaultHealthCheckInterval)
	logHealth.Printf("Creating health monitor: interval=%v, maxRestartFailures=%d", interval, maxConsecutiveRestartFailures)
	return &HealthMonitor{
		launcher:            l,
		interval:            interval,
		stopCh:              make(chan struct{}),
		doneCh:              make(chan struct{}),
		consecutiveFailures: util.NewFailureCounter[string](),
	}
}

// Start begins periodic health checks in a background goroutine.
func (hm *HealthMonitor) Start() {
	logHealth.Printf("Starting health monitor: interval=%v", hm.interval)
	logger.LogInfo("startup", "Health monitor started (interval=%v)", hm.interval)
	go hm.run()
}

// Stop signals the health monitor to stop and waits for it to finish.
func (hm *HealthMonitor) Stop() {
	logHealth.Print("Stopping health monitor, waiting for background goroutine to finish")
	close(hm.stopCh)
	<-hm.doneCh
	logHealth.Print("Health monitor stopped")
	logger.LogInfo("shutdown", "Health monitor stopped")
}

func (hm *HealthMonitor) run() {
	defer close(hm.doneCh)
	logHealth.Printf("Health monitor goroutine started: interval=%v", hm.interval)

	ticker := time.NewTicker(hm.interval)
	defer ticker.Stop()

	for {
		select {
		case <-hm.stopCh:
			logHealth.Print("Health monitor goroutine exiting: stop requested")
			return
		case <-hm.launcher.ctx.Done():
			logHealth.Print("Health monitor goroutine exiting: launcher context cancelled")
			return
		case <-ticker.C:
			hm.checkAll()
		}
	}
}

// checkAll iterates over every configured backend and attempts to restart
// any server that is in an error state.
func (hm *HealthMonitor) checkAll() {
	serverIDs := hm.launcher.ServerIDs()
	logHealth.Printf("Running health check: checking %d servers", len(serverIDs))
	for _, serverID := range serverIDs {
		state := hm.launcher.GetServerState(serverID)

		switch state.Status {
		case "error":
			hm.handleErrorState(serverID, state)
		case "running":
			// Reset consecutive failure counter on healthy server.
			if previous := hm.consecutiveFailures.Reset(serverID); previous > 0 {
				logHealth.Printf("Server recovered: resetting failure counter for serverID=%s (was %d)", serverID, previous)
			}
		}
	}
}

func (hm *HealthMonitor) handleErrorState(serverID string, state ServerState) {
	failures := hm.consecutiveFailures.Get(serverID)
	if failures >= maxConsecutiveRestartFailures {
		// Already logged when the threshold was reached; stay silent.
		logHealth.Printf("Skipping restart for serverID=%s: max failures reached (%d/%d)", serverID, failures, maxConsecutiveRestartFailures)
		return
	}

	logger.LogWarn("backend", "Health check: server %q in error state (%s), attempting restart (%d/%d)",
		serverID, state.LastError, failures+1, maxConsecutiveRestartFailures)

	logHealth.Printf("Attempting restart of serverID=%s: previousFailures=%d", serverID, failures)
	// Clear error state and cached connection so GetOrLaunch can retry.
	hm.launcher.clearServerForRestart(serverID)

	_, err := GetOrLaunch(hm.launcher, serverID)
	if err != nil {
		failures = hm.consecutiveFailures.Increment(serverID)
		logHealth.Printf("Restart failed for serverID=%s: consecutiveFailures=%d, err=%v", serverID, failures, err)
		logger.LogError("backend", "Health check: restart failed for server %q: %v (attempt %d/%d)",
			serverID, err, failures, maxConsecutiveRestartFailures)
		if failures >= maxConsecutiveRestartFailures {
			logger.LogError("backend",
				"Health check: server %q reached max restart attempts (%d), will not retry until gateway restart",
				serverID, maxConsecutiveRestartFailures)
		}
		return
	}

	hm.consecutiveFailures.Reset(serverID)
	logHealth.Printf("Successfully restarted server: serverID=%s", serverID)
	logger.LogInfo("backend", "Health check: successfully restarted server %q", serverID)
}
