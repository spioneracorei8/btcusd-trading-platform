package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spioneracorei8/btcusd-trading-platform/server/helper"
)

// dotEnvName is the file every process looks for.
const dotEnvName = ".env"

// dotEnvSearchDepth is how many directories up to look.
//
// The binaries are usually run from the repository root, from server/, or from
// server/<cmd>/ during development. Three levels covers all of them and stops
// well short of wandering into a parent project's unrelated .env.
const dotEnvSearchDepth = 3

// dotEnvLookup returns a lookup that consults the process environment first
// and the repository's .env second, along with the path it found.
//
// # Why a process reads this at all
//
// Configuration comes from the environment (12-factor) and in production it
// does: compose passes the file explicitly, and the systemd unit names it by
// absolute path. On a developer machine there is no such wrapper, so every
// `go run ./backtest` had to be preceded by sourcing the same file by hand —
// and forgetting produced either a refusal to start or, worse, a run against
// whatever variables happened to be exported from an earlier session.
//
// # Why the environment wins
//
// A container has its values from compose and must not have them
// second-guessed by a file that happened to be in the image; an operator who
// exported something for one command must get that value. The file fills gaps,
// it does not assert.
//
// # Why nothing is written back
//
// An earlier version called os.Setenv for each missing key. That worked, and
// it made Load() mutate global state — which made its result depend on
// whatever had called it earlier in the same process. The tests caught it
// immediately by leaking values between cases, and the same order-dependence
// would be far harder to see in a binary. Composing a lookup keeps the file's
// values scoped to the one configuration being built.
//
// A missing file is not an error. It is the normal case inside a container.
func dotEnvLookup() (helper.LookupFunc, string, error) {
	path, ok := findDotEnv()
	if !ok {
		return os.LookupEnv, "", nil
	}

	entries, err := parseDotEnv(path)
	if err != nil {
		return nil, "", err
	}

	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		values[entry.key] = entry.value
	}

	return func(key string) (string, bool) {
		if value, ok := os.LookupEnv(key); ok {
			return value, true
		}
		value, ok := values[key]
		return value, ok
	}, path, nil
}

// findDotEnv walks up from the working directory.
func findDotEnv() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}

	for range dotEnvSearchDepth + 1 {
		candidate := filepath.Join(dir, dotEnvName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// dotEnvEntry is one assignment, kept in file order.
type dotEnvEntry struct{ key, value string }

// parseDotEnv reads KEY=value lines.
//
// Deliberately not a shell: the file is read, never executed, so a stray
// backtick or $(...) in a generated password cannot run anything. That matters
// because setup.sh generates the production password into this file on a host
// nobody logs into.
func parseDotEnv(path string) ([]dotEnvEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	var entries []dotEnvEntry
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// `export KEY=value` is common in files people also source.
		line = strings.TrimPrefix(line, "export ")

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}

		value = strings.TrimSpace(value)
		// One layer of surrounding quotes, so a value with a trailing comment
		// marker or spaces can be written the way a shell would accept it.
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		entries = append(entries, dotEnvEntry{key: key, value: value})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return entries, nil
}
