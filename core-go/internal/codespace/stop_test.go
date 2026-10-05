package codespace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/harneet2512/gtm-work/core-go/internal/demorun"
)

func TestStopStopsTheServicesThenSweepsOrphanedNeo4jJVMs(t *testing.T) {
	var order []string
	neo := `D:\ghost-demo\neo4j`
	list := func(context.Context) ([]demorun.Process, error) {
		order = append(order, "list")
		return []demorun.Process{{PID: 41, Name: "java.exe", CommandLine: neo + `\lib`}, {PID: 42, Name: "java.exe", CommandLine: "java elsewhere"}}, nil
	}
	var lines []string
	err := StopAll(context.Background(), func(context.Context) error { order = append(order, "down"); return nil }, list,
		func(pid int) error { order = append(order, "kill"); return nil }, []string{neo}, func(f string, a ...any) { lines = append(lines, f) })
	if err != nil || strings.Join(order, ",") != "down,list,kill" {
		t.Fatalf("order = %v err = %v", order, err)
	}
	if !strings.Contains(strings.Join(lines, "|"), "orphaned") {
		t.Fatalf("the sweep is reported: %v", lines)
	}
}

func TestStopReportsBothAFailedStopAndAFailedSweep(t *testing.T) {
	list := func(context.Context) ([]demorun.Process, error) { return nil, errors.New("no process list") }
	err := StopAll(context.Background(), func(context.Context) error { return errors.New("core will not stop") }, list,
		func(int) error { return nil }, []string{"x"}, nil)
	if err == nil || !strings.Contains(err.Error(), "core will not stop") || !strings.Contains(err.Error(), "no process list") {
		t.Fatalf("both failures are reported: %v", err)
	}
}
