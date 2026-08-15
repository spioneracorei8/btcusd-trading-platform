package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/server/config"
)

// chdir moves into dir for the duration of a test.
func chdir(t *testing.T, dir string) {
	t.Helper()

	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
}

// completeEnv is every variable a non-serving process needs, minus the one
// under test in each case.
const completeEnv = `APP_ENV=dev
LOG_LEVEL=info
DATABASE_URL=postgres://trading:trading@localhost:5432/btcusd?sslmode=disable
MARKET_SYMBOL=BTCUSDT
MARKET_TYPE=spot
MARKET_TIMEFRAMES=1m,5m
FEE_TAKER_PCT=0.05
SLIPPAGE_TICKS=1
MARKET_TICK_SIZE=0.01
`

// writeEnv creates a .env in dir.
func writeEnv(t *testing.T, dir, content string) string {
	t.Helper()

	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	return path
}

// TestTheCLIDoesNotNeedAListenPort.
//
// The backtest binary opens no socket. Demanding HTTP_PORT from it turned a
// read-only analysis tool into something that refused to start over a setting
// it would never use, which is the kind of friction that gets worked around
// with a shell alias nobody else has.
func TestTheCLIDoesNotNeedAListenPort(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, completeEnv) // deliberately no HTTP_PORT
	chdir(t, dir)

	if _, err := config.Load(config.WithoutHTTPServer()); err != nil {
		t.Fatalf("Load(WithoutHTTPServer) returned error: %v", err)
	}
}

// TestAServingProcessStillNeedsOne. The relaxation is opt-in: the api must not
// start without knowing where to listen.
func TestAServingProcessStillNeedsOne(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, completeEnv)
	chdir(t, dir)

	_, err := config.Load()
	if err == nil {
		t.Fatal("a serving process started with no HTTP_PORT")
	}
	if !strings.Contains(err.Error(), "HTTP_PORT") {
		t.Errorf("the error does not name HTTP_PORT: %v", err)
	}
}

// TestAPresentPortIsStillValidatedForTheCLI.
//
// Optional is not the same as ignored. A typo in a variable this process does
// not use is still worth reporting, because the same file is read by the api,
// where it decides whether anything can connect at all.
func TestAPresentPortIsStillValidatedForTheCLI(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, completeEnv+"HTTP_PORT=not-a-number\n")
	chdir(t, dir)

	_, err := config.Load(config.WithoutHTTPServer())
	if err == nil {
		t.Fatal("a malformed HTTP_PORT was accepted because the CLI does not serve")
	}
	if !strings.Contains(err.Error(), "HTTP_PORT") {
		t.Errorf("the error does not name HTTP_PORT: %v", err)
	}
}

// TestTheEnvironmentFileIsFoundFromASubdirectory, because the binaries are run
// from the repository root, from server/, and from server/<cmd>/ in turn.
func TestTheEnvironmentFileIsFoundFromASubdirectory(t *testing.T) {
	root := t.TempDir()
	path := writeEnv(t, root, completeEnv)

	nested := filepath.Join(root, "server", "backtest")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	chdir(t, nested)

	cfg, err := config.Load(config.WithoutHTTPServer())
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.EnvFile != path {
		t.Errorf("loaded %q, want %q", cfg.EnvFile, path)
	}
	if cfg.Market.Symbol != "BTCUSDT" {
		t.Errorf("symbol is %q; the file was not applied", cfg.Market.Symbol)
	}
}

// TestTheEnvironmentAlwaysWinsOverTheFile.
//
// A container has its values from compose and must not have them
// second-guessed by a file that happened to be in the image, and an operator
// who exported something for one command must get that value. The file fills
// gaps; it does not assert.
func TestTheEnvironmentAlwaysWinsOverTheFile(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, completeEnv)
	chdir(t, dir)

	t.Setenv("MARKET_SYMBOL", "ETHUSDT")

	cfg, err := config.Load(config.WithoutHTTPServer())
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.Market.Symbol != "ETHUSDT" {
		t.Errorf("symbol is %q, want the exported ETHUSDT — the file overrode the environment",
			cfg.Market.Symbol)
	}
}

// TestAMissingFileIsNotAnError. It is the normal case inside a container,
// where compose supplies everything.
func TestAMissingFileIsNotAnError(t *testing.T) {
	chdir(t, t.TempDir())

	for _, entry := range []struct{ key, value string }{
		{"APP_ENV", "dev"}, {"LOG_LEVEL", "info"},
		{"DATABASE_URL", "postgres://trading:trading@localhost:5432/btcusd?sslmode=disable"},
		{"MARKET_SYMBOL", "BTCUSDT"}, {"MARKET_TYPE", "spot"},
		{"MARKET_TIMEFRAMES", "1m"}, {"FEE_TAKER_PCT", "0.05"},
		{"SLIPPAGE_TICKS", "1"}, {"MARKET_TICK_SIZE", "0.01"},
	} {
		t.Setenv(entry.key, entry.value)
	}

	cfg, err := config.Load(config.WithoutHTTPServer())
	if err != nil {
		t.Fatalf("Load() returned error with no .env present: %v", err)
	}
	if cfg.EnvFile != "" {
		t.Errorf("EnvFile is %q with no file present", cfg.EnvFile)
	}
}

// TestTheFileIsReadNotExecuted.
//
// setup.sh generates the production password into this file on a host nobody
// logs into. A value containing shell metacharacters must arrive verbatim
// rather than being run, and quoting a value must not leave the quotes in it.
func TestTheFileIsReadNotExecuted(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, `APP_ENV=dev
LOG_LEVEL=info
DATABASE_URL="postgres://trading:p$(whoami)`+"`id`"+`@localhost:5432/btcusd?sslmode=disable"
export MARKET_SYMBOL=BTCUSDT
MARKET_TYPE=spot
MARKET_TIMEFRAMES=1m
FEE_TAKER_PCT=0.05
SLIPPAGE_TICKS=1
MARKET_TICK_SIZE=0.01
`)
	chdir(t, dir)

	cfg, err := config.Load(config.WithoutHTTPServer())
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if !strings.Contains(cfg.Database.URL, "$(whoami)") || !strings.Contains(cfg.Database.URL, "`id`") {
		t.Errorf("the connection string was expanded rather than taken literally: %q", cfg.Database.URL)
	}
	if strings.HasPrefix(cfg.Database.URL, `"`) {
		t.Errorf("the surrounding quotes were kept: %q", cfg.Database.URL)
	}
	// `export KEY=value` is common in files people also source by hand.
	if cfg.Market.Symbol != "BTCUSDT" {
		t.Errorf("symbol is %q; an `export` prefix was not handled", cfg.Market.Symbol)
	}
}

// TestCommentsAndBlankLinesAreIgnored, since .env.example is mostly comments
// and is the file people copy.
func TestCommentsAndBlankLinesAreIgnored(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, "# a heading\n\n"+completeEnv+"\n# trailing note\n")
	chdir(t, dir)

	if _, err := config.Load(config.WithoutHTTPServer()); err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
}

// TestTheShippedExampleIsSufficient.
//
// `cp .env.example .env` is step one of the quick start and the first thing
// setup.sh does on the VPS. If the example were missing a required value, that
// instruction would be wrong everywhere it appears.
func TestTheShippedExampleIsSufficient(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", ".env.example"))
	if err != nil {
		t.Skipf("no .env.example alongside the module: %v", err)
	}

	dir := t.TempDir()
	writeEnv(t, dir, string(example))
	chdir(t, dir)

	if _, err := config.Load(); err != nil {
		t.Errorf("a straight copy of .env.example does not satisfy the loader: %v", err)
	}
}
