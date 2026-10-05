package neo4jtest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"time"
)

// SupportedJava lists the major Java versions Neo4j 5.26 LTS runs on.
var SupportedJava = []int{17, 21}

var javaVersionRE = regexp.MustCompile(`version "(\d+)(?:\.(\d+))?`)

// parseJavaMajor extracts the major version from `java -version` output ("21.0.4", "17", "1.8.0_402").
func parseJavaMajor(out string) (int, error) {
	m := javaVersionRE.FindStringSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("neo4jtest: cannot read a Java version from %q", out)
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("neo4jtest: bad Java major %q: %w", m[1], err)
	}
	if major == 1 && m[2] != "" { // 1.8 style
		if major, err = strconv.Atoi(m[2]); err != nil {
			return 0, fmt.Errorf("neo4jtest: bad Java minor %q: %w", m[2], err)
		}
	}
	return major, nil
}

func isSupportedJava(major int) bool {
	for _, v := range SupportedJava {
		if v == major {
			return true
		}
	}
	return false
}

// findJava returns the java executable and its major version. It prefers JAVA_HOME, then PATH.
// A missing or unsupported Java is reported as a skip error that says exactly what was found.
func findJava(ctx context.Context) (path string, major int, err error) {
	exe := "java"
	if runtime.GOOS == "windows" {
		exe = "java.exe"
	}
	var candidates []string
	if home := os.Getenv("JAVA_HOME"); home != "" {
		candidates = append(candidates, filepath.Join(home, "bin", exe))
	}
	if p, lookErr := exec.LookPath("java"); lookErr == nil {
		candidates = append(candidates, p)
	}
	if len(candidates) == 0 {
		return "", 0, skipf("Java %v not found (no JAVA_HOME, no java on PATH); install a JDK or point NEO4J_TEST_URI at a running Neo4j", SupportedJava)
	}
	var seen []string
	for _, c := range candidates {
		vctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		out, runErr := exec.CommandContext(vctx, c, "-version").CombinedOutput()
		cancel()
		if runErr != nil {
			seen = append(seen, fmt.Sprintf("%s (does not run: %v)", c, runErr))
			continue
		}
		v, parseErr := parseJavaMajor(string(out))
		if parseErr != nil {
			seen = append(seen, fmt.Sprintf("%s (%v)", c, parseErr))
			continue
		}
		if isSupportedJava(v) {
			return c, v, nil
		}
		seen = append(seen, fmt.Sprintf("%s (Java %d)", c, v))
	}
	return "", 0, skipf("no usable Java: Neo4j 5.26 needs Java %v, found %v", SupportedJava, seen)
}
