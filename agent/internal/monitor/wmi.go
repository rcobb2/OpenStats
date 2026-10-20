//go:build windows

package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"runtime"
	"sync"
	"sync/atomic"
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
	onElevated      func(pid uint32, exeName, exePath, user string)
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
		onElevated:      cfg.OnElevated,
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

// FilterProcesses removes processes that would be excluded by this watcher's
// patterns. Used to filter the results of ScanExistingProcesses at startup.
func (w *WMIWatcher) FilterProcesses(procs []RunningProcess) []RunningProcess {
	filtered := make([]RunningProcess, 0, len(procs))
	for _, p := range procs {
		if !w.isExcluded(p.ExeName) && !isAgentProcess(p.ExeName, p.ExePath) {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// wmiSubscription owns the COM objects behind one pair of event subscriptions.
type wmiSubscription struct {
	locator                           *ole.IUnknown
	wmi, service, startSink, stopSink *ole.IDispatch
}

// close releases everything in reverse order of creation. It must only be
// called once no goroutine is still using the sinks.
func (s *wmiSubscription) close() {
	for _, d := range []*ole.IDispatch{s.stopSink, s.startSink, s.service, s.wmi} {
		if d != nil {
			d.Release()
		}
	}
	if s.locator != nil {
		s.locator.Release()
	}
}

// subscribe connects to WMI and subscribes to process start and stop traces.
// On error nothing is leaked.
func (w *WMIWatcher) subscribe() (*wmiSubscription, error) {
	s := &wmiSubscription{}
	fail := func(format string, err error) (*wmiSubscription, error) {
		s.close()
		return nil, fmt.Errorf(format+": %w", err)
	}

	var err error
	s.locator, err = oleutil.CreateObject("WbemScripting.SWbemLocator")
	if err != nil {
		return fail("failed to create WMI locator", err)
	}
	s.wmi, err = s.locator.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return fail("failed to get WMI dispatch", err)
	}
	serviceRaw, err := oleutil.CallMethod(s.wmi, "ConnectServer")
	if err != nil {
		return fail("failed to connect to WMI", err)
	}
	s.service = serviceRaw.ToIDispatch()

	startSinkRaw, err := oleutil.CallMethod(s.service, "ExecNotificationQuery", "SELECT * FROM Win32_ProcessStartTrace")
	if err != nil {
		return fail("failed to subscribe to process start events", err)
	}
	s.startSink = startSinkRaw.ToIDispatch()

	stopSinkRaw, err := oleutil.CallMethod(s.service, "ExecNotificationQuery", "SELECT * FROM Win32_ProcessStopTrace")
	if err != nil {
		return fail("failed to subscribe to process stop events", err)
	}
	s.stopSink = stopSinkRaw.ToIDispatch()
	return s, nil
}

// initCOM initializes COM for this goroutine; S_FALSE (already initialized) is fine.
func initCOM() (uninit bool, err error) {
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		oleerr, ok := err.(*ole.OleError)
		if !ok || oleerr.Code() != 0x00000001 { // S_FALSE = already initialized
			return false, fmt.Errorf("COM init failed: %w", err)
		}
	}
	return true, nil
}

// Run keeps WMI event subscriptions alive until ctx is cancelled. A failure to
// initialize COM or subscribe, or an event loop failing with anything other
// than a NextEvent timeout, tears the subscription down and retries with a
// 1s..30s doubling backoff, so process tracking never silently stops.
func (w *WMIWatcher) Run(ctx context.Context) error {
	bo := newBackoff(retryBackoffMin, retryBackoffMax)

	for ctx.Err() == nil {
		gotEvent, err := w.runSubscription(ctx)
		if ctx.Err() != nil {
			break
		}
		if gotEvent {
			bo.Reset()
		}
		delay := bo.Next()
		w.logger.Warn("WMI subscription failed; will re-subscribe", "error", err, "retryIn", delay)
		if !sleepCtx(ctx, delay) {
			break
		}
	}
	w.logger.Info("WMI watcher shutting down")
	return nil
}

// runSubscription performs one COM init + subscribe + event-processing cycle.
// It returns when ctx is done or either event loop fails; gotEvent reports
// whether at least one event was delivered (used to reset the backoff).
func (w *WMIWatcher) runSubscription(ctx context.Context) (gotEvent bool, err error) {
	// COM init/uninit must happen on the same OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	uninit, err := initCOM()
	if err != nil {
		return false, err
	}
	if uninit {
		defer ole.CoUninitialize()
	}

	sub, err := w.subscribe()
	if err != nil {
		return false, err
	}
	defer sub.close()

	w.logger.Info("WMI event subscriptions active")

	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var seen atomic.Bool
	errc := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	// Pass the shared service connection so per-event helpers don't open new connections.
	go func() { defer wg.Done(); errc <- w.processStartEvents(loopCtx, sub.startSink, sub.service, &seen) }()
	go func() { defer wg.Done(); errc <- w.processStopEvents(loopCtx, sub.stopSink, &seen) }()

	// First loop to end (error or ctx) stops the other; wait for both before
	// the deferred close releases the COM objects they use.
	first := <-errc
	cancel()
	wg.Wait()
	return seen.Load(), first
}

func (w *WMIWatcher) processStartEvents(ctx context.Context, sink *ole.IDispatch, svc *ole.IDispatch, seen *atomic.Bool) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// NextEvent with a 1 second timeout so we can check ctx cancellation.
		eventRaw, timedOut, err := nextEvent(sink)
		if err != nil {
			if timedOut {
				continue // no event in window; expected
			}
			return fmt.Errorf("WMI start event wait failed: %w", err)
		}
		seen.Store(true)
		event := eventRaw.ToIDispatch()
		if event == nil {
			continue
		}

		processName := getStringProp(event, "ProcessName")
		processID := getUint32Prop(event, "ProcessID")
		parentProcessID := getUint32Prop(event, "ParentProcessID")

		event.Release()

		if processName == "" {
			continue
		}

		// Elevation detection runs before the exclude-pattern filtering below
		// — mirrors the macOS design, for the same reason: the most common
		// escalation targets are terminals and shells (cmd.exe,
		// powershell.exe, pwsh.exe, WindowsTerminal.exe), and every one of
		// those is in the default agent.yaml's exclude list as noise for
		// usage tracking. Filtering before this check ran meant elevating
		// cmd.exe produced nothing at all — confirmed on real hardware.
		// Checked here (not in ScanExistingProcesses) so processes already
		// elevated when the agent starts are adopted, not counted — a
		// restart must not manufacture elevation events. Deliberately not
		// gated on isNewGroup: an elevated child joining an existing process
		// group is still a distinct UAC consent; the parent-token check
		// inside isUACElevatedLaunch is the inheritance dedup.
		if w.onElevated != nil {
			eval := evaluateUACElevation(processID, parentProcessID)
			// Field-diagnostic breadcrumb, same spirit as the macOS side's
			// "evaluated possible elevation" log: only logged for processes
			// with an actual (non-Limited) token state worth explaining, to
			// avoid flooding this at ordinary process-start volume.
			if eval.procKnown && eval.procType != tokenElevationTypeLimited {
				w.logger.Debug("evaluated possible elevation",
					"pid", processID, "exe", processName, "ppid", parentProcessID,
					"procType", eval.procType, "parentKnown", eval.parentKnown, "parentType", eval.parentType,
					"counted", eval.counted)
			} else if !eval.procKnown {
				w.logger.Debug("could not read token for elevation check",
					"pid", processID, "exe", processName, "failStep", eval.procFailStep, "failErr", eval.procFailErr)
			}
			// Pseudo-console hosts (OpenConsole.exe, conhost.exe) ride along
			// with an elevated shell as a side effect of one UAC consent —
			// counting them separately double-counts a single user action
			// and pollutes reports with a process no one chose to run.
			if eval.counted && !isIncidentalConsoleHost(processName) {
				exePath := getProcessExePath(svc, processID)
				user := getProcessUser(svc, processID)
				w.onElevated(processID, processName, exePath, user)
			}
		}

		if w.isExcluded(processName) {
			continue
		}

		// Reuse the shared WMI service connection to avoid opening a new COM
		// connection per event, which is the dominant cost under load.
		exePath := getProcessExePath(svc, processID)

		// The session helper is our own binary running as the user; it is not
		// application usage (see selfexclude.go).
		if isAgentProcess(processName, exePath) {
			continue
		}

		var familyKey string
		if w.familyResolver != nil {
			familyKey = w.familyResolver(processName, exePath)
		}

		user := getProcessUser(svc, processID)

		isNewGroup := w.tracker.OnProcessStart(processID, parentProcessID, processName, exePath, user, familyKey)

		if w.onStart != nil {
			w.onStart(processID, processName, isNewGroup)
		}
	}
}

func (w *WMIWatcher) processStopEvents(ctx context.Context, sink *ole.IDispatch, seen *atomic.Bool) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		eventRaw, timedOut, err := nextEvent(sink)
		if err != nil {
			if timedOut {
				continue
			}
			return fmt.Errorf("WMI stop event wait failed: %w", err)
		}
		seen.Store(true)
		event := eventRaw.ToIDispatch()
		if event == nil {
			continue
		}

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
	// VARIANT.ToString() copies the string out but doesn't free the
	// underlying COM-allocated BSTR — that's what Clear() (VariantClear) is
	// for. Without it, every call leaks one BSTR; called per process-start
	// event for the agent's entire service uptime (plus once per running
	// process at startup via ScanExistingProcesses), that's a slow, steady
	// native-memory leak over a potentially weeks-long run.
	defer val.Clear()
	return val.ToString()
}

func getUint32Prop(dispatch *ole.IDispatch, name string) uint32 {
	val, err := oleutil.GetProperty(dispatch, name)
	if err != nil {
		return 0
	}
	defer val.Clear()
	return uint32(val.Val)
}

// getProcessExePath looks up the executable path for a running process via WMI.
// svc must be an already-connected SWbemServices dispatch pointer; it is reused
// to avoid opening a new COM connection per call.
func getProcessExePath(svc *ole.IDispatch, pid uint32) string {
	query := fmt.Sprintf("SELECT ExecutablePath FROM Win32_Process WHERE ProcessId = %d", pid)
	resultRaw, err := oleutil.CallMethod(svc, "ExecQuery", query)
	if err != nil {
		return ""
	}
	result := resultRaw.ToIDispatch()
	if result == nil {
		return ""
	}
	defer result.Release()

	countVar, err := oleutil.GetProperty(result, "Count")
	if err != nil {
		return ""
	}
	defer countVar.Clear()
	if countVar.Val == 0 {
		return ""
	}

	itemRaw, err := oleutil.CallMethod(result, "ItemIndex", 0)
	if err != nil {
		return ""
	}
	item := itemRaw.ToIDispatch()
	if item == nil {
		return ""
	}
	defer item.Release()

	return getStringProp(item, "ExecutablePath")
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
	if countVar != nil {
		countVar.Clear()
	}
	logger.Debug("scanning existing processes", "count", count)

	for i := 0; i < count; i++ {
		itemRaw, err := oleutil.CallMethod(result, "ItemIndex", i)
		if err != nil {
			continue
		}
		item := itemRaw.ToIDispatch()
		if item == nil {
			continue
		}

		pid := uint32(getUint32Prop(item, "ProcessId"))
		parentPID := uint32(getUint32Prop(item, "ParentProcessId"))
		exeName := getStringProp(item, "Name")
		exePath := getStringProp(item, "ExecutablePath")

		item.Release()

		if exeName == "" {
			continue
		}

		user := getProcessUser(service, pid)

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
// svc must be an already-connected SWbemServices dispatch pointer; it is reused
// to avoid opening a new COM connection per call.
func getProcessUser(svc *ole.IDispatch, pid uint32) string {
	// ExecMethod calls GetOwner on the process object path.
	// GetOwner has no in-parameters; it returns User and Domain as out-params.
	objPath := fmt.Sprintf("Win32_Process.Handle='%d'", pid)
	outRaw, err := oleutil.CallMethod(svc, "ExecMethod", objPath, "GetOwner")
	if err != nil {
		return ""
	}
	outParams := outRaw.ToIDispatch()
	if outParams == nil {
		return ""
	}
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

// nextEventTimeoutMs is the NextEvent wait window.
const nextEventTimeoutMs = 1000

// nextEvent waits up to nextEventTimeoutMs for one event. timedOut is true for
// the benign "no event in the window" result: either the error classifies as a
// WMI timeout, or the call blocked for (nearly) the whole window, which a
// broken sink never does — it fails immediately. The second test keeps an
// unexpected timeout encoding from being mistaken for a dead subscription,
// which would otherwise tear down and resubscribe every second and drop events.
func nextEvent(sink *ole.IDispatch) (raw *ole.VARIANT, timedOut bool, err error) {
	start := time.Now()
	raw, err = oleutil.CallMethod(sink, "NextEvent", nextEventTimeoutMs)
	if err != nil {
		timedOut = isWMITimeout(err) || time.Since(start) >= nextEventTimeoutMs*9/10*time.Millisecond
	}
	return raw, timedOut, err
}
