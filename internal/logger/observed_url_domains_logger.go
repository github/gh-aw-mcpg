package logger

import (
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/github/gh-aw-mcpg/internal/util"
)

var logObservedDomains = ForFile()

const observedURLDomainsFileName = "observed-url-domains.json"

var urlDomainAuditEnabled atomic.Bool

// SetURLDomainAuditEnabled toggles URL domain observation for middleware and guards.
func SetURLDomainAuditEnabled(enabled bool) {
	logObservedDomains.Printf("URL domain audit enabled=%v", enabled)
	urlDomainAuditEnabled.Store(enabled)
}

// URLDomainAuditEnabled reports whether URL domain observation is enabled.
func URLDomainAuditEnabled() bool {
	return urlDomainAuditEnabled.Load()
}

// ObservedURLDomainsLogger manages unique observed URL domains grouped by server ID.
type ObservedURLDomainsLogger struct {
	lockable
	jsonFileSink
	data map[string]map[string]struct{}
}

var (
	globalObservedURLDomainsLogger *ObservedURLDomainsLogger
	globalObservedURLDomainsMu     sync.RWMutex
	observedURLDomainsLoggerRef    = bindGlobalLogger(&globalObservedURLDomainsMu, &globalObservedURLDomainsLogger)
)

var observedURLDomainsLoggerFactory = newLoggerFactory(
	func(file *os.File, logDir, fileName string) (*ObservedURLDomainsLogger, error) {
		if file != nil {
			file.Close()
		}

		logObservedDomains.Printf("Initializing observed URL domains logger: logDir=%s, fileName=%s", logDir, fileName)
		l := &ObservedURLDomainsLogger{
			jsonFileSink: jsonFileSink{logDir: logDir, fileName: fileName},
			data:         make(map[string]map[string]struct{}),
		}
		if err := l.writeJSON(make(map[string][]string), 0600); err != nil {
			return nil, err
		}
		log.Printf("Observed URL domains logging to file: %s", filepath.Join(logDir, fileName))
		return l, nil
	},
	func(err error, logDir, fileName string) (*ObservedURLDomainsLogger, error) {
		return fallbackLoggerOnInitError(err, "Failed to initialize observed URL domains log file", "Observed URL domains logging disabled", &ObservedURLDomainsLogger{
			jsonFileSink: jsonFileSink{logDir: logDir, fileName: fileName, useFallback: true},
			data:         make(map[string]map[string]struct{}),
		})
	},
)

// InitObservedURLDomainsLogger initializes observed-url-domains.json logger.
func InitObservedURLDomainsLogger(logDir, fileName string) error {
	return observedURLDomainsLoggerRef.initWithFallback(logDir, fileName, os.O_TRUNC, observedURLDomainsLoggerFactory)
}

// LogDomains logs unique domains for a server ID.
func (l *ObservedURLDomainsLogger) LogDomains(serverID string, domains []string) error {
	if serverID == "" || len(domains) == 0 {
		return nil
	}

	return l.withLock(func() error {
		if l.useFallback {
			logObservedDomains.Printf("Skipping domain logging in fallback mode: serverID=%s", serverID)
			return nil
		}

		serverDomains, ok := l.data[serverID]
		if !ok {
			serverDomains = make(map[string]struct{})
			l.data[serverID] = serverDomains
		}

		changed := false
		for _, d := range domains {
			if d == "" {
				continue
			}
			if _, exists := serverDomains[d]; exists {
				continue
			}
			serverDomains[d] = struct{}{}
			changed = true
		}

		if !changed {
			logObservedDomains.Printf("No new domains observed: serverID=%s, candidates=%d", serverID, len(domains))
			return nil
		}
		logObservedDomains.Printf("Persisting newly observed domains: serverID=%s, serverDomainCount=%d, servers=%d", serverID, len(serverDomains), len(l.data))
		serialized := make(map[string][]string, len(l.data))
		for serverID, serverDomains := range l.data {
			serialized[serverID] = util.SortedSetKeys(serverDomains)
		}
		return l.writeJSON(serialized, 0600)
	})
}

func (l *ObservedURLDomainsLogger) Close() error { return nil }

// LogObservedURLDomains appends newly observed domains for a server.
func LogObservedURLDomains(serverID string, domains []string) {
	withGlobalLogger(&globalObservedURLDomainsMu, &globalObservedURLDomainsLogger, func(l *ObservedURLDomainsLogger) {
		if err := l.LogDomains(serverID, domains); err != nil {
			log.Printf("WARNING: Failed to log observed URL domains for server %s: %v", serverID, err)
		}
	})
}
