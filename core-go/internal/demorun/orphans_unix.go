//go:build !windows

package demorun

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ListProcesses lists the machine's processes with their command lines (ps).
func ListProcesses(ctx context.Context) ([]Process, error) {
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,comm=,args=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var procs []Process
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		procs = append(procs, Process{PID: pid, Name: f[1], CommandLine: strings.Join(f[2:], " ")})
	}
	return procs, nil
}
