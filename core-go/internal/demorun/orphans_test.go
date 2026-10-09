package demorun

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOrphansAreJavaProcessesWhoseCommandLinePointsUnderTheNeo4jDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "ghost-demo", "neo4j")
	other := filepath.Join(t.TempDir(), "elsewhere", "neo4j")
	procs := []Process{
		{PID: 11, Name: "java.exe", CommandLine: `"C:\jdk\bin\java.exe" -cp "` + filepath.Join(home, "lib", "*") + `" org.neo4j.server.CommunityEntryPoint`},
		{PID: 12, Name: "java.exe", CommandLine: strings.ToUpper(filepath.Join(home, "conf"))}, // Windows paths are case-insensitive
		{PID: 13, Name: "java.exe", CommandLine: "java -jar " + filepath.ToSlash(filepath.Join(home, "plugins", "x.jar"))},
		{PID: 14, Name: "java.exe", CommandLine: "java -cp " + filepath.Join(other, "lib")}, // another Neo4j, not ours
		{PID: 15, Name: "node.exe", CommandLine: "node " + filepath.Join(home, "x.js")},     // not java
		{PID: 16, Name: "java", CommandLine: "java -cp " + home + "-backup"},                // a sibling directory sharing the prefix
		{PID: 0, Name: "java.exe", CommandLine: home},
	}
	got := OrphanPIDs(procs, home)
	want := []int{11, 13}
	if runtimeFoldsCase() {
		want = []int{11, 12, 13}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orphans = %v, want %v", got, want)
	}
	if got := OrphanPIDs(procs, ""); len(got) != 0 {
		t.Fatalf("with no directory nothing is an orphan: %v", got)
	}
}

func TestSweepKillsOnlyTheOrphansAndReportsWhatItKilled(t *testing.T) {
	home := filepath.Join(t.TempDir(), "neo4j")
	lister := func(context.Context) ([]Process, error) {
		return []Process{{PID: 21, Name: "java.exe", CommandLine: home + "/lib"}, {PID: 22, Name: "java.exe", CommandLine: "java other"}}, nil
	}
	var killed []int
	n, err := SweepOrphans(context.Background(), lister, func(pid int) error { killed = append(killed, pid); return nil }, home)
	if err != nil || n != 1 || !reflect.DeepEqual(killed, []int{21}) {
		t.Fatalf("sweep = %d %v killed %v", n, err, killed)
	}
}

func TestSweepSurfacesListingAndKillFailures(t *testing.T) {
	home := filepath.Join(t.TempDir(), "neo4j")
	if _, err := SweepOrphans(context.Background(), func(context.Context) ([]Process, error) { return nil, errors.New("no powershell") },
		func(int) error { return nil }, home); err == nil || !strings.Contains(err.Error(), "no powershell") {
		t.Fatalf("listing: %v", err)
	}
	lister := func(context.Context) ([]Process, error) {
		return []Process{{PID: 31, Name: "java.exe", CommandLine: home}, {PID: 32, Name: "java.exe", CommandLine: home}}, nil
	}
	n, err := SweepOrphans(context.Background(), lister, func(pid int) error {
		if pid == 31 {
			return errors.New("access denied")
		}
		return nil
	}, home)
	if err == nil || !strings.Contains(err.Error(), "access denied") || n != 1 {
		t.Fatalf("one kill fails, the other still goes: %d %v", n, err)
	}
}

func TestTheSystemListerReturnsProcessesOfThisMachine(t *testing.T) {
	procs, err := ListProcesses(context.Background())
	if err != nil || len(procs) == 0 {
		t.Fatalf("ListProcesses = %d, %v", len(procs), err)
	}
	for _, p := range procs {
		if p.Name == "" {
			t.Fatalf("incomplete entry %+v", p)
		}
	}
}
