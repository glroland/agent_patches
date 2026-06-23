//go:build windows

package main

import (
	"context"

	"golang.org/x/sys/windows/svc"
)

type agentSvc struct{ run func(context.Context) }

func (s *agentSvc) Execute(_ []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); s.run(ctx) }()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for c := range r {
		if c.Cmd == svc.Stop || c.Cmd == svc.Shutdown {
			break
		}
		status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	}
	status <- svc.Status{State: svc.StopPending}
	cancel()
	<-done
	return false, 0
}

func isWindowsService() (bool, error) {
	return svc.IsWindowsService()
}

func runAsWindowsService(fn func(context.Context)) error {
	return svc.Run("agent_patches", &agentSvc{run: fn})
}
