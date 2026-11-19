package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	stopTimeout = 5 * time.Second
	maxLogLines = 250
)

type Manager struct {
	cli        happCLI
	configPath string

	mu                   sync.Mutex
	connection           *connection
	changingSubscription bool
	shuttingDown         bool
	server               string
	logs                 []string
}

type connection struct {
	cmd      *exec.Cmd
	done     chan struct{}
	stopping bool
}

type status struct {
	Running  bool     `json:"running"`
	Stopping bool     `json:"stopping"`
	Server   string   `json:"server"`
	Logs     []string `json:"logs"`
}

func (m *Manager) status() status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return status{
		Running:  m.connection != nil,
		Stopping: m.connection != nil && m.connection.stopping,
		Server:   m.server,
		Logs:     append([]string{}, m.logs...),
	}
}

func (m *Manager) Start(server string) error {
	server = strings.TrimSpace(server)
	if server == "" {
		server = "1"
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	switch {
	case m.shuttingDown:
		return errors.New("shutting down")
	case m.connection != nil:
		return errors.New("already connected; disconnect first")
	case m.changingSubscription:
		return errors.New("subscription change in progress")
	}

	cmd := m.cli.connect(server)
	stdout := &lineWriter{log: m.appendLog}
	stderr := &lineWriter{log: m.appendLog, prefix: "error: "}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start happ: %w", err)
	}

	active := &connection{cmd: cmd, done: make(chan struct{})}
	m.connection = active
	m.server = server
	m.appendLogLocked("Starting server: " + server)
	if err := saveConfig(m.configPath, Config{Server: server}); err != nil {
		m.appendLogLocked("Save configuration: " + err.Error())
	}
	go m.wait(active, stdout, stderr)
	return nil
}

func (m *Manager) wait(active *connection, stdout, stderr *lineWriter) {
	err := active.cmd.Wait()
	stdout.flush()
	stderr.flush()

	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.appendLogLocked("HAPP stopped: " + err.Error())
	} else {
		m.appendLogLocked("HAPP stopped")
	}
	m.connection = nil
	close(active.done)
}

func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.connection == nil {
		return nil
	}
	if m.connection.stopping {
		return errors.New("already stopping")
	}
	m.stopLocked(m.connection)
	return nil
}

func (m *Manager) beginShutdown() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shuttingDown = true
	active := m.connection
	if active == nil {
		return nil
	}
	if !active.stopping {
		m.stopLocked(active)
	}
	return active.done
}

func (m *Manager) stopLocked(active *connection) {
	active.stopping = true
	if err := active.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		m.appendLogLocked("Interrupt HAPP: " + err.Error())
	}
	go func() {
		timer := time.NewTimer(stopTimeout)
		defer timer.Stop()
		select {
		case <-active.done:
		case <-timer.C:
			if err := active.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				m.appendLog("Kill HAPP: " + err.Error())
			}
		}
	}()
}

func (m *Manager) useSubscription(ctx context.Context, name string) error {
	m.mu.Lock()
	switch {
	case m.shuttingDown:
		m.mu.Unlock()
		return errors.New("shutting down")
	case m.connection != nil:
		m.mu.Unlock()
		return errors.New("disconnect before changing subscription")
	case m.changingSubscription:
		m.mu.Unlock()
		return errors.New("subscription change in progress")
	}
	m.changingSubscription = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.changingSubscription = false
		m.mu.Unlock()
	}()
	_, err := m.cli.run(ctx, "sub", "use", name)
	return err
}

func (m *Manager) appendLog(line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appendLogLocked(line)
}

func (m *Manager) appendLogLocked(line string) {
	m.logs = append(m.logs, time.Now().Format("15:04:05")+" "+line)
	if len(m.logs) > maxLogLines {
		m.logs = m.logs[len(m.logs)-maxLogLines:]
	}
}
