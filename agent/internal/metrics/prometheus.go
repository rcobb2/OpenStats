package metrics

import (
	"os"

	"github.com/prometheus/client_golang/prometheus"
)

const namespace = "openlabstats"

var hostname string

func init() {
	hostname, _ = os.Hostname()
}

// Metrics holds all Prometheus metric collectors for the agent.
type Metrics struct {
	AppUsageSeconds *prometheus.CounterVec
	AppForegroundSeconds *prometheus.CounterVec
	AppLaunches     *prometheus.CounterVec
	AppActive       *prometheus.GaugeVec
	UserSessionActive       *prometheus.GaugeVec
	UserSessionDuration     *prometheus.GaugeVec
	UserSessionLogins       *prometheus.CounterVec
	UserSessionSecondsTotal *prometheus.CounterVec
	DeviceInfo              *prometheus.GaugeVec
	InstalledSoftware       *prometheus.GaugeVec
}

// New creates and registers all Prometheus metrics.
func New() *Metrics {
	m := &Metrics{
		AppUsageSeconds: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "app_usage_seconds_total",
				Help:      "Total seconds an application has been running.",
			},
			[]string{"app", "exe", "category", "user", "hostname"},
		),
		AppForegroundSeconds: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "app_foreground_seconds_total",
				Help:      "Total seconds an application has been actively in the foreground.",
			},
			[]string{"app", "exe", "category", "user", "hostname"},
		),
		AppLaunches: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "app_launches_total",
				Help:      "Total number of times an application has been launched.",
			},
			[]string{"app", "exe", "category", "user", "hostname"},
		),
		AppActive: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "app_active",
				Help:      "Whether an application is currently running (1) or not (0).",
			},
			[]string{"app", "exe", "category", "user", "hostname"},
		),
		UserSessionActive: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "user_session_active",
				Help:      "Whether a user session is active (1) or not (0).",
			},
			[]string{"user", "hostname"},
		),
		UserSessionDuration: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "user_session_duration_seconds",
				Help:      "Duration of the current user session in seconds.",
			},
			[]string{"user", "hostname"},
		),
		UserSessionLogins: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "user_session_logins_total",
				Help:      "Total number of user login sessions.",
			},
			[]string{"user", "hostname"},
		),
		UserSessionSecondsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "user_session_seconds_total",
				Help:      "Total seconds users have been signed in.",
			},
			[]string{"user", "hostname"},
		),
		DeviceInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "device_info",
				Help:      "Static device information as labels. Value is always 1.",
			},
			[]string{"hostname", "os_version", "os_build", "domain", "model", "manufacturer", "serial"},
		),
		InstalledSoftware: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "installed_software_info",
				Help:      "Installed software inventory. Value is always 1.",
			},
			[]string{"name", "version", "publisher", "hostname"},
		),
	}

	prometheus.MustRegister(
		m.AppUsageSeconds,
		m.AppForegroundSeconds,
		m.AppLaunches,
		m.AppActive,
		m.UserSessionActive,
		m.UserSessionDuration,
		m.UserSessionLogins,
		m.UserSessionSecondsTotal,
		m.DeviceInfo,
		m.InstalledSoftware,
	)

	return m
}

// Hostname returns the cached hostname.
func Hostname() string {
	return hostname
}
