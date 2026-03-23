//go:build windows

package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const ServiceName = "OpenLabStats"
const ServiceDisplayName = "OpenLabStats Agent"
const ServiceDescription = "Open-source software usage tracking agent for higher education"

// handler implements svc.Handler for the Windows Service Control Manager.
type handler struct {
	run    AgentRunner
	logger *slog.Logger
}

func (h *handler) Execute(args []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const acceptedCmds = svc.AcceptStop | svc.AcceptShutdown

	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run the agent in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		errCh <- h.run(ctx)
	}()

	status <- svc.Status{State: svc.Running, Accepts: acceptedCmds}
	h.logger.Info("service started")

	for {
		select {
		case err := <-errCh:
			if err != nil {
				h.logger.Error("agent exited with error", "error", err)
				return false, 1
			}
			return false, 0

		case req := <-requests:
			switch req.Cmd {
			case svc.Stop, svc.Shutdown:
				h.logger.Info("service stop requested")
				status <- svc.Status{State: svc.StopPending}
				cancel()
				// Give agent up to 15 seconds to shut down gracefully.
				select {
				case <-errCh:
				case <-time.After(15 * time.Second):
					h.logger.Warn("graceful shutdown timed out")
				}
				return false, 0

			case svc.Interrogate:
				status <- req.CurrentStatus
			}
		}
	}
}

// Run starts the agent as a Windows service or in console mode.
func Run(runner AgentRunner, logger *slog.Logger) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return fmt.Errorf("failed to determine if running as service: %w", err)
	}

	if isService {
		return svc.Run(ServiceName, &handler{run: runner, logger: logger})
	}

	// Console mode - run directly.
	logger.Info("running in console mode (not a Windows service)")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle Ctrl+C in console mode.
	go func() {
		sigCh := make(chan os.Signal, 1)
		// signal.Notify not needed here; we rely on context cancellation.
		// For console mode, we'll handle it in main.
		<-sigCh
		cancel()
	}()

	return runner(ctx)
}

// Install registers the agent as a Windows service.
func Install(exePath string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %s already exists", ServiceName)
	}

	s, err = m.CreateService(ServiceName, exePath, mgr.Config{
		DisplayName: ServiceDisplayName,
		Description: ServiceDescription,
		StartType:   mgr.StartAutomatic,
	}, "service")
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	defer s.Close()

	// Set up event log source.
	if err := eventlog.InstallAsEventCreate(ServiceName, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil {
		s.Delete()
		return fmt.Errorf("failed to install event log source: %w", err)
	}

	return nil
}

// Uninstall removes the Windows service registration.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to service manager: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(ServiceName)
	if err != nil {
		return fmt.Errorf("service %s not found: %w", ServiceName, err)
	}
	defer s.Close()

	if err := s.Delete(); err != nil {
		return fmt.Errorf("failed to delete service: %w", err)
	}

	if err := eventlog.Remove(ServiceName); err != nil {
		return fmt.Errorf("failed to remove event log source: %w", err)
	}

	return nil
}
