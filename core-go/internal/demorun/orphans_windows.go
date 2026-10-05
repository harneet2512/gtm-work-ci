//go:build windows

package demorun

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// ListProcesses lists the machine's processes with their command lines (CIM: the command line is not in the process table).
func ListProcesses(ctx context.Context) ([]Process, error) {
	script := `Get-CimInstance Win32_Process | Select-Object ProcessId,Name,CommandLine | ConvertTo-Json -Compress`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("powershell Get-CimInstance: %w", err)
	}
	var rows []struct {
		ProcessID   int     `json:"ProcessId"`
		Name        string  `json:"Name"`
		CommandLine *string `json:"CommandLine"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("read the process list: %w", err)
	}
	procs := make([]Process, 0, len(rows))
	for _, r := range rows {
		p := Process{PID: r.ProcessID, Name: r.Name}
		if r.CommandLine != nil {
			p.CommandLine = *r.CommandLine
		}
		procs = append(procs, p)
	}
	return procs, nil
}
