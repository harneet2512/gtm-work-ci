package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// loggedCodespaceCommands are the long, unattended ones whose progress and failure must survive a launcher that gives the
// process no usable stdout or stderr (the detached record run: its redirected log was empty and the exit-1 reason was lost).
var loggedCodespaceCommands = map[string]bool{"setup": true, "up": true, "down": true, "record": true, "drive": true, "reset": true, "baseline": true}

// codespaceLog tees a codespace command's output into <demoHome>/logs/codespace-<command>.log, which the command opens itself,
// and returns finish, which records how the command ended (including the error main would print to a lost stderr).
// Anything that stops the file being opened degrades to the plain writer: logging never fails the command.
func codespaceLog(home, command string, out io.Writer) (io.Writer, func(error)) {
	nop := func(error) {}
	if home == "" || !loggedCodespaceCommands[command] {
		return out, nop
	}
	dir := filepath.Join(home, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return out, nop
	}
	f, err := os.OpenFile(filepath.Join(dir, "codespace-"+command+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return out, nop
	}
	stamp := func() string { return time.Now().Format(time.RFC3339) }
	fmt.Fprintf(f, "--- ghostctl codespace %s started %s (pid %d) ---\n", command, stamp(), os.Getpid())
	finish := func(err error) {
		defer f.Close()
		if err != nil {
			fmt.Fprintf(f, "--- ghostctl codespace %s FAILED %s: %v ---\n", command, stamp(), err)
			return
		}
		fmt.Fprintf(f, "--- ghostctl codespace %s finished %s ---\n", command, stamp())
	}
	if out == nil {
		return f, finish
	}
	return io.MultiWriter(f, out), finish // the file first: MultiWriter stops at the first failing writer, and a lost stdout must not starve the log
}
