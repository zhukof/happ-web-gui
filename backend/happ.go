package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const happWaitDelay = time.Second

type happCLI struct {
	binary  string
	dataDir string
}

func (h happCLI) args(args ...string) []string {
	return append([]string{"--home", h.dataDir}, args...)
}

func (h happCLI) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, h.binary, h.args(args...)...)
	cmd.WaitDelay = happWaitDelay
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("happ: %w", ctxErr)
	}
	if message := strings.TrimSpace(string(output)); message != "" {
		return "", fmt.Errorf("happ: %s (%w)", message, err)
	}
	return "", fmt.Errorf("happ: %w", err)
}

func (h happCLI) connect(server string) *exec.Cmd {
	cmd := exec.Command(h.binary, h.args("connect", server, "--mode", "proxy", "--socks", "10808", "--http", "10809")...)
	cmd.WaitDelay = happWaitDelay
	return cmd
}
