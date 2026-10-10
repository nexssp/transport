//go:build windows

package examples_test

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func killProcessGroup(pid int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// #nosec G204 -- pid is cmd.Process.Pid from the just-started nflow
	// process, an int produced by the OS; it is not user-controlled input.
	_ = exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
