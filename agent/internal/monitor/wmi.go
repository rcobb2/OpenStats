package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

// WMIWatcher subscribes to Win32_ProcessStartTrace and Win32_ProcessStopTrace
// events and feeds them into a Tracker.
type WMIWatcher struct {
	tracker         *Tracker
	logger          *slog.Logger
	excludePatterns []*regexp.Regexp
	minLifetime     time.Duration
	familyResolver  func(exeName, exePath string) string // returns family key
	onStart         func(pid uint32, exeName string, isNewGroup bool)
	onStop          func(session *ProcessSession)
}

// WMIWatcherConfig holds configuration for the WMI watcher.
type WMIWatcherConfig struct {
	ExcludePatterns []string
	MinLifetime     time.Duration
	FamilyResolver  func(exeName, exePath string) string
	OnStart         func(pid uint32, exeName string, isNewGroup bool)
	OnStop          func(session *ProcessSession)
}

// NewWMIWatcher creates a new WMI event watcher.
func NewWMIWatcher(tracker *Tracker, logger *slog.Logger, cfg WMIWatcherConfig) (*WMIWatcher, error) {
	patterns := make([]*regexp.Regexp, 0, len(cfg.ExcludePatterns))
	for _, p := range cfg.ExcludePatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid exclude pattern %q: %w", p, err)
		}
		patterns = append(patterns, re)
	}

	return &WMIWatcher{
		tracker:         tracker,
		logger:          logger,
		excludePatterns: patterns,
		minLifetime:     cfg.MinLifetime,
		familyResolver:  cfg.FamilyResolver,
		onStart:         cfg.OnStart,
		onStop:          cfg.OnStop,
	}, nil
}

// isExcluded checks if a process name matches any exclude pattern.
func (w *WMIWatcher) isExcluded(exeName string) bool {
	for _, re := range w.excludePatterns {
		if re.MatchString(exeName) {
			return true
		}
	}
	return false
}

// Run starts WMI event subscriptions. Blocks until ctx is cancelled.
func (w *WMIWatcher) Run(ctx context.Context) error {
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		// Already initialized is OK.
		oleerr, ok := err.(*ole.OleError)
		if !ok || oleerr.Code() != 0x00000001 { // S_FALSE = already initialized
			return fmt.Errorf("COM init failed: %w", err)
		}
	}
	defer ole.CoUninitialize()

	locator, err := oleutil.CreateObject("WbemScripting.SWbemLocator")
	if err != nil {
		return fmt.Errorf("failed to create WMI locator: %w", err)
	}
	defer locator.Release()

	wmi, err := locator.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return fmt.Errorf("failed to get WMI dispatch: %w", err)
	}
	defer wmi.Release()

	serviceRaw, err := oleutil.CallMethod(wmi, "ConnectServer")
	if err != nil {
		return fmt.Errorf("failed to connect to WMI: %w", err)
	}
	service := serviceRaw.ToIDispatch()
	defer service.Release()

	// Subscribe to process start events.
	startQuery := "SELECT * FROM Win32_ProcessStartTrace"
	startSinkRaw, err := oleutil.CallMethod(service, "ExecNotificationQuery", startQuery)
	if err != nil {
		return fmt.Errorf("failed to subscribe to process start events: %w", err)
	}
	startSink := startSinkRaw.ToIDispatch()
	defer startSink.Release()

	// Subscribe to process stop events.
	stopQuery := "SELECT * FROM Win32_ProcessStopTrace"
	stopSinkRaw, err := oleutil.CallMethod(service, "ExecNotificationQuery", stopQuery)
	if err != nil {
		return fmt.Errorf("failed to subscribe to process stop events: %w", err)
	}
	stopSink := stopSinkRaw.ToIDispatch()
	defer stopSink.Release()

	w.logger.Info("WMI event subscriptions active")

	// Process events in separate goroutines.
	go w.processStartEvents(ctx, startSink)
	go w.processStopEvents(ctx, stopSink)

	<-ctx.Done()
	w.logger.Info("WMI watcher shutting down")
	return nil
}

func (w *WMIWatcher) processStartEvents(ctx context.Context, sink *ole.IDispatch) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// NextEvent with 1 second timeout so we can check ctx cancellation.
		eventRaw, err := oleutil.CallMethod(sink, "NextEvent", 1000)
		if err != nil {
			// Timeout is expected, just retry.
			continue
		}
		event := eventRaw.ToIDispatch()

		processName := getStringProp(event, "ProcessName")
		processID := getUint32Prop(event, "ProcessID")
		parentProcessID := getUint32Prop(event, "ParentProcessID")

		event.Release()

		if processName == "" || w.isExcluded(processName) {
			continue
		}

		// Look up the executable path from the running process.
		exePath := getProcessExePath(processID)

		// Resolve family key for normalizer-based grouping.
		var familyKey string
		if w.familyResolver != nil {
			familyKey = w.familyResolver(processName, exePath)
		}

		// Resolve the user for this process.
		user := getProcessUser(processID)

		isNewGroup := w.tracker.OnProcessStart(processID, parentProcessID, processName, exePath, user, familyKey)

		if w.onStart != nil {
			w.onStart(processID, processName, isNewGroup)
		}
	}
}

func (w *WMIWatcher) processStopEvents(ctx context.Context, sink *ole.IDispatch) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		eventRaw, err := oleutil.CallMethod(sink, "NextEvent", 1000)
		if err != nil {
			continue
		}
		event := eventRaw.ToIDispatch()

		processID := getUint32Prop(event, "ProcessID")

		event.Release()

		session := w.tracker.OnProcessStop(processID)
		if session == nil {
			continue
		}

		// Filter out processes that lived less than minLifetime.
		if w.minLifetime > 0 && session.StopTime.Sub(session.StartTime) < w.minLifetime {
			continue
		}

		if w.onStop != nil {
			w.onStop(session)
		}
	}
}

// Helper functions to extract WMI event properties.

func getStringProp(dispatch *ole.IDispatch, name string) string {
	val, err := oleutil.GetProperty(dispatch, name)
	if err != nil {
		return ""
	}
	return val.ToString()
}

func getUint32Prop(dispatch *ole.IDispatch, name string) uint32 {
	val, err := oleutil.GetProperty(dispatch, name)
	if err != nil {
		return 0
	}
	return uint32(val.Val)
}

// getProcessExePath looks up the executable path for a running process via WMI.
func getProcessExePath(pid uint32) string {
	// Use a quick WMI query to get the ExecutablePath.
	// This is best-effort; if the process exits before we query, we get empty.
	locator, err := oleutil.CreateObject("WbemScripting.SWbemLocator")
	if err != nil {
		return ""
	}
	defer locator.Release()

	wmi, err := locator.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return ""
	}
	defer wmi.Release()

	serviceRaw, err := oleutil.CallMethod(wmi, "ConnectServer")
	if err != nil {
		return ""
	}
	svc := serviceRaw.ToIDispatch()
	defer svc.Release()

	query := fmt.Sprintf("SELECT ExecutablePath FROM Win32_Process WHERE ProcessId = %d", pid)
	resultRaw, err := oleutil.CallMethod(svc, "ExecQuery", query)
	if err != nil {
		return ""
	}
	result := resultRaw.ToIDispatch()
	defer result.Release()

	countVar, err := oleutil.GetProperty(result, "Count")
	if err != nil || countVar.Val == 0 {
		return ""
	}

	itemRaw, err := oleutil.CallMethod(result, "ItemIndex", 0)
	if err != nil {
		return ""
	}
	item := itemRaw.ToIDispatch()
	defer item.Release()

	return getStringProp(item, "ExecutablePath")
}

// RunningProcess represents a process discovered during startup scan.
type RunningProcess struct {
	PID       uint32
	ParentPID uint32
	ExeName   string
	ExePath   string
	User      string
	FamilyKey string
}

// ScanExistingProcesses queries Win32_Process for all running processes.
// This should be called at startup to discover processes that started before the agent.
func ScanExistingProcesses(logger *slog.Logger, familyResolver func(string, string) string) []RunningProcess {
	var processes []RunningProcess

	// Initialize COM for this goroutine.
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		oleerr, ok := err.(*ole.OleError)
		if !ok || oleerr.Code() != 0x00000001 {
			logger.Error("COM init failed for scan", "error", err)
			return processes
		}
	}
	defer ole.CoUninitialize()

	locator, err := oleutil.CreateObject("WbemScripting.SWbemLocator")
	if err != nil {
		logger.Error("failed to create WMI locator for scan", "error", err)
		return processes
	}
	defer locator.Release()

	wmi, err := locator.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		logger.Error("failed to get WMI dispatch for scan", "error", err)
		return processes
	}
	defer wmi.Release()

	serviceRaw, err := oleutil.CallMethod(wmi, "ConnectServer")
	if err != nil {
		logger.Error("failed to connect to WMI for scan", "error", err)
		return processes
	}
	service := serviceRaw.ToIDispatch()
	defer service.Release()

	query := "SELECT ProcessId, ParentProcessId, Name, ExecutablePath FROM Win32_Process"
	resultRaw, err := oleutil.CallMethod(service, "ExecQuery", query)
	if err != nil {
		logger.Error("failed to query processes", "error", err)
		return processes
	}
	result := resultRaw.ToIDispatch()
	defer result.Release()

	countVar, _ := oleutil.GetProperty(result, "Count")
	count := int(countVar.Val)
	logger.Debug("scanning existing processes", "count", count)

	for i := 0; i < count; i++ {
		itemRaw, err := oleutil.CallMethod(result, "ItemIndex", i)
		if err != nil {
			continue
		}
		item := itemRaw.ToIDispatch()

		pid := uint32(getUint32Prop(item, "ProcessId"))
		parentPID := uint32(getUint32Prop(item, "ParentProcessId"))
		exeName := getStringProp(item, "Name")
		exePath := getStringProp(item, "ExecutablePath")

		item.Release()

		if exeName == "" {
			continue
		}

		user := getProcessUser(pid)

		var familyKey string
		if familyResolver != nil {
			familyKey = familyResolver(exeName, exePath)
		}

		processes = append(processes, RunningProcess{
			PID:       pid,
			ParentPID: parentPID,
			ExeName:   exeName,
			ExePath:   exePath,
			User:      user,
			FamilyKey: familyKey,
		})
	}

	logger.Info("finished scanning existing processes", "count", len(processes))
	return processes
}

// getProcessUser looks up the user who owns a process via WMI GetOwner.
func getProcessUser(pid uint32) string {
	// Best-effort: call GetOwner on the Win32_Process object.
	// If the process exits before we can query it, we return empty.
	locator, err := oleutil.CreateObject("WbemScripting.SWbemLocator")
	if err != nil {
		return ""
	}
	defer locator.Release()

	wmi, err := locator.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return ""
	}
	defer wmi.Release()

	serviceRaw, err := oleutil.CallMethod(wmi, "ConnectServer")
	if err != nil {
		return ""
	}
	svc := serviceRaw.ToIDispatch()
	defer svc.Release()

	// ExecMethod calls GetOwner on the process object path.
	// GetOwner has no in-parameters; it returns User and Domain as out-params.
	objPath := fmt.Sprintf("Win32_Process.Handle='%d'", pid)
	outRaw, err := oleutil.CallMethod(svc, "ExecMethod", objPath, "GetOwner")
	if err != nil {
		return ""
	}
	outParams := outRaw.ToIDispatch()
	defer outParams.Release()

	user := getStringProp(outParams, "User")
	if user == "" {
		return ""
	}

	// Include DOMAIN\user format for disambiguation.
	domain := getStringProp(outParams, "Domain")
	if domain != "" {
		return domain + "\\" + user
	}
	return user
}
