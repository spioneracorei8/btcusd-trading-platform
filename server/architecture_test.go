package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spioneracorei8/btcusd-trading-platform/server/config"
)

// The import rules from CLAUDE.md §5, checked by walking the source.
//
// They are written down in three places — CLAUDE.md, ADR 0005 and ADR 0014 —
// and documentation cannot enforce anything. A layering rule that is only
// described decays quietly: nothing breaks the day it is violated, and by the
// time it matters the violation is load-bearing.
//
// These tests parse imports rather than reasoning about them, so a rule either
// holds or names the file that broke it.

const modulePath = "github.com/spioneracorei8/btcusd-trading-platform/server"

// projectImports returns the in-project packages a file imports, as paths
// relative to the module root.
func projectImports(t *testing.T, path string) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var out []string
	for _, spec := range file.Imports {
		value, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("%s: bad import %s", path, spec.Path.Value)
		}
		if trimmed, ok := strings.CutPrefix(value, modulePath+"/"); ok {
			out = append(out, trimmed)
		}
	}
	return out
}

// goFilesIn lists the non-test Go files directly inside a directory.
func goFilesIn(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read %s: %v", dir, err)
	}

	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	return files
}

// serviceDirs lists every services/<domain> directory.
func serviceDirs(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir("services")
	if err != nil {
		t.Fatalf("read services: %v", err)
	}

	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join("services", entry.Name()))
		}
	}
	if len(dirs) == 0 {
		t.Fatal("no services found; the test is looking in the wrong place")
	}
	return dirs
}

// TestConstantsImportsNothingFromTheProject. Every layer depends on constants,
// so anything it imported would be reachable from everywhere and a cycle would
// be one edit away.
func TestConstantsImportsNothingFromTheProject(t *testing.T) {
	for _, file := range goFilesIn(t, "constants") {
		if imports := projectImports(t, file); len(imports) > 0 {
			t.Errorf("%s imports %v; constants must import nothing from this project", file, imports)
		}
	}
}

// TestModelsImportsOnlyConstants. An entity that could drag a layer in behind
// it would make every package that names one depend on that layer too.
func TestModelsImportsOnlyConstants(t *testing.T) {
	for _, file := range goFilesIn(t, "models") {
		for _, imported := range projectImports(t, file) {
			if imported != "constants" {
				t.Errorf("%s imports %q; models may import only constants", file, imported)
			}
		}
	}
}

// TestUsecasesKnowNoSQL is the rule that keeps the business logic testable
// without a database, and keeps the repository free to change how rows are
// stored. It is also what lets the backtest engine be verified entirely
// against fixtures.
func TestUsecasesKnowNoSQL(t *testing.T) {
	forbidden := []string{"github.com/jackc/pgx", modulePath + "/database"}

	for _, service := range serviceDirs(t) {
		dir := filepath.Join(service, "usecase")

		for _, file := range goFilesIn(t, dir) {
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			for _, spec := range parsed.Imports {
				value, _ := strconv.Unquote(spec.Path.Value)
				for _, bad := range forbidden {
					if strings.HasPrefix(value, bad) {
						t.Errorf("%s imports %q; a usecase talks to a repository interface, never to SQL",
							file, value)
					}
				}
			}
		}
	}
}

// TestServiceInterfaceFilesReachOnlyModelsAndConstants.
//
// CLAUDE.md §5 says a service's interface file may import models and constants
// only. Taken literally that also forbids one interface file naming another —
// backtest.RunParams carrying a strategy.Strategy, or a trend.Filter — which
// is not what the rule is protecting against.
//
// What matters is the transitive closure. The rule exists to stop an interface
// file depending on a *layer*: on a handler, a usecase, a repository, or the
// database. An interface file that names another interface file drags in
// nothing but models and constants, because that is all the other one is
// allowed to import either. Go imports packages, not directories, so a sibling
// usecase/ package existing next to it changes nothing about what is pulled in.
//
// So this walks the graph instead of allow-listing names. A package qualifies
// if everything it can reach is models, constants, or another package that
// qualifies. That makes the exception self-maintaining: the day a package at a
// service root starts importing its own usecase, it stops qualifying and every
// interface file that named it fails here, which is exactly when someone
// should be told.
func TestServiceInterfaceFilesReachOnlyModelsAndConstants(t *testing.T) {
	for _, service := range serviceDirs(t) {
		for _, file := range goFilesIn(t, service) {
			for _, imported := range projectImports(t, file) {
				if reason := unreachableReason(t, imported, nil); reason != "" {
					t.Errorf("%s imports %q, which %s.\n"+
						"A service's interface file may only reach models, constants, and\n"+
						"other packages that reach nothing more. If a type has to cross a\n"+
						"service boundary, move it to models — indicator.Snapshot became\n"+
						"models.IndicatorSnapshot for exactly this reason.",
						file, imported, reason)
				}
			}
		}
	}
}

// unreachableReason returns why a package may not be reached from an interface
// file, or "" when it may be.
func unreachableReason(t *testing.T, pkg string, seen map[string]bool) string {
	t.Helper()

	if pkg == "models" || pkg == "constants" {
		return ""
	}
	for _, layer := range []string{"/handler", "/usecase", "/repository"} {
		if strings.Contains(pkg+"/", layer+"/") {
			return "is an implementation package"
		}
	}
	if pkg == "database" || strings.HasPrefix(pkg, "database/") {
		return "is the database layer"
	}

	if seen == nil {
		seen = map[string]bool{}
	}
	if seen[pkg] {
		return "" // already being checked further up the walk
	}
	seen[pkg] = true

	files := goFilesIn(t, pkg)
	if len(files) == 0 {
		return "is not a package this test can read"
	}

	for _, file := range files {
		for _, imported := range projectImports(t, file) {
			if reason := unreachableReason(t, imported, seen); reason != "" {
				return fmt.Sprintf("reaches %q, which %s", imported, reason)
			}
		}
	}
	return ""
}

// TestNoServiceReachesAnotherServicesRepository.
//
// A usecase may drive another service's usecase — market and backtest both
// drive candle — because that is sideways rather than backwards. Reaching past
// it into the repository is what is forbidden: it bypasses the rules that live
// in between, and the rule being bypassed here would be "a candle that is
// still forming is never stored".
func TestNoServiceReachesAnotherServicesRepository(t *testing.T) {
	for _, service := range serviceDirs(t) {
		own := filepath.ToSlash(service) + "/repository"

		for _, layer := range []string{"", "handler", "usecase"} {
			dir := filepath.Join(service, layer)

			for _, file := range goFilesIn(t, dir) {
				for _, imported := range projectImports(t, file) {
					if !strings.Contains(imported, "/repository") {
						continue
					}
					if imported == own || strings.HasPrefix(imported, own+"/") {
						continue
					}
					t.Errorf("%s imports %q, another service's repository.\n"+
						"Go through that service's usecase instead; reaching past it skips\n"+
						"the rules that live in between.", file, imported)
				}
			}
		}
	}
}

// TestOnlyTheWiringKnowsImplementations.
//
// server.go and the three entry points are allowed to name implementation
// packages, because something has to. Everything else depends on interfaces,
// which is what makes a fake possible in a test and a swap possible in
// production.
func TestOnlyTheWiringKnowsImplementations(t *testing.T) {
	// Files permitted to import implementation subpackages.
	wiring := map[string]bool{
		"server/server.go":  true,
		"main.go":           true,
		"collector/main.go": true,
		"backtest/main.go":  true,
		"reconcile/main.go": true,
		"testhelper":        true,
	}

	for _, service := range serviceDirs(t) {
		for _, file := range goFilesIn(t, service) {
			for _, imported := range projectImports(t, file) {
				for _, layer := range []string{"/handler/", "/usecase/", "/repository/"} {
					if strings.Contains(imported+"/", layer) && !wiring[file] {
						t.Errorf("%s imports the implementation package %q; interface files depend on interfaces",
							file, imported)
					}
				}
			}
		}
	}
}

// TestEveryCostFieldIsWiredFromConfiguration.
//
// # What this prevents
//
// backtest.Costs has eleven fields. There used to be four constructors: the
// backtest CLI set all eleven, and the collector, the API and the reconcile
// CLI each set three. An empty Model reads as percentage, so a spread
// configured venue was priced as percentage-with-taker in three of the four —
// and the reconciliation reported that cost-model difference as a verdict on
// the strategy. Everything rendered normally.
//
// There is now one constructor, Config.BacktestCosts. This walks what it
// returns and fails on any field left at its zero value, so a field added to
// Costs and not wired here fails immediately rather than defaulting into a
// number nobody checks.
//
// It is a reflection test rather than a list of field names on purpose: a list
// is a second place to forget.
func TestEveryCostFieldIsWiredFromConfiguration(t *testing.T) {
	// Every cost variable set to a distinctive non-zero value, so a field that
	// arrives zero can only have arrived that way by not being wired.
	env := map[string]string{
		"APP_ENV": "dev", "LOG_LEVEL": "info", "HTTP_PORT": "8080",
		"DATABASE_URL": "postgres://u:p@localhost:5432/d?sslmode=disable",

		"FEE_TAKER_PCT": "0.07", "FEE_MAKER_PCT": "0.03",
		"SLIPPAGE_TICKS": "3", "MARKET_TICK_SIZE": "0.05",
		"COST_MODEL": "spread", "SPREAD_POINTS": "1700", "POINT_VALUE": "0.02",
		"CONTRACT_SIZE": "2", "MIN_LOT": "0.03", "LOT_STEP": "0.04",
		"COMMISSION_PER_LOT": "6",
	}

	cfg, err := config.LoadFrom(func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	})
	if err != nil {
		t.Fatalf("LoadFrom() returned error: %v", err)
	}

	costs := reflect.ValueOf(cfg.BacktestCosts())
	for i := range costs.NumField() {
		field := costs.Type().Field(i)
		if costs.Field(i).IsZero() {
			t.Errorf("Costs.%s is zero after Config.BacktestCosts(), though %s was configured "+
				"to a non-zero value.\n"+
				"Every field has to be wired there: an unwired one does not fail, it takes a\n"+
				"default — and an unwired Model silently prices a spread venue as percentage.",
				field.Name, field.Name)
		}
	}
}

// allGoFiles walks the whole module and returns every Go file, tests included.
func allGoFiles(t *testing.T) []string {
	t.Helper()

	var files []string
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" || entry.Name() == "db" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no Go files found; the test is looking in the wrong place")
	}
	return files
}

// TestNothingReachesATradingEndpoint is CLAUDE.md §1, which is the one rule in
// this repository with no exception and no discussion attached: the system
// never places an order.
//
// It is checked mechanically because the danger is not that someone decides to
// add trading. It is that the code to do it arrives one harmless-looking piece
// at a time — a client method "for completeness", a constant "so it is
// documented" — and each piece is individually defensible. This test makes the
// first piece fail, which is the only point at which refusing is easy.
//
// Phase 06 is where the pressure starts: from here the system has an opinion
// about the market, and the distance between having an opinion and acting on
// it is one HTTP call.
func TestNothingReachesATradingEndpoint(t *testing.T) {
	// Binance's order, account and withdrawal paths. Matching on the URL
	// fragment rather than on words like "order" avoids flagging the many
	// legitimate uses of "order" in this codebase — sort order, bar order.
	forbidden := []string{
		"/api/v3/order",
		"/api/v3/openOrders",
		"/api/v3/allOrders",
		"/api/v3/account",
		"/sapi/v1/capital/withdraw",
		"/fapi/v1/order",
		"/fapi/v1/positionSide",
		"/fapi/v1/leverage",
		"/wapi/v3/withdraw",
	}

	for _, path := range allGoFiles(t) {
		// This file names the paths in order to forbid them.
		if filepath.Base(path) == "architecture_test.go" {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, endpoint := range forbidden {
			if strings.Contains(string(content), endpoint) {
				t.Errorf("%s references %q. CLAUDE.md §1: this system never places an order, "+
					"and market data needs no such endpoint. If there is a reason for this, "+
					"it is a conversation to have before the code exists, not after.",
					path, endpoint)
			}
		}
	}
}

// TestTheOnlyGoogleScopeIsSendingMessages.
//
// Phase 07 gave this system its first credential that is not read-only: a
// service account key, on a host whose other job is reading public market
// data. The scope it asks for is the whole of what that key can do, so it is
// pinned here rather than left to whoever next edits the client.
//
// firebase.messaging cannot reach an order, an account or a withdrawal — no
// such endpoint exists in Firebase. A broader scope would be a credential able
// to do more than the thing it was issued for, which is the shape of the
// mistake CLAUDE.md §1 exists to prevent.
func TestTheOnlyGoogleScopeIsSendingMessages(t *testing.T) {
	const permitted = "https://www.googleapis.com/auth/firebase.messaging"

	// Any googleapis.com/auth/... scope string, quoted in Go source.
	scope := regexp.MustCompile(`https://www\.googleapis\.com/auth/[A-Za-z0-9._-]+`)

	for _, path := range allGoFiles(t) {
		if filepath.Base(path) == "architecture_test.go" {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		for _, found := range scope.FindAllString(string(content), -1) {
			if found != permitted {
				t.Errorf("%s asks Google for %q. The only scope this system may hold is %q: "+
					"it is a credential on a host that otherwise only reads public market "+
					"data, and a wider one could do more than the thing it was issued for.",
					path, found, permitted)
			}
		}
	}
}

// TestNoCodePathBranchesOnBeingABacktest is CLAUDE.md §3.2 made checkable.
//
// The rule is that live and backtest differ only in where the bars come from.
// A branch on which mode is running breaks that in the worst possible way: the
// backtest keeps passing, keeps reporting, and stops describing what live will
// do — and the divergence is invisible precisely because the numbers still look
// reasonable.
//
// The check is textual and therefore approximate. It looks for a condition
// testing a backtest/live flag, which is the shape the mistake takes. A
// determined author can evade it; someone adding the branch for convenience at
// 2am cannot, and that is who the rule is for.
func TestNoCodePathBranchesOnBeingABacktest(t *testing.T) {
	// A condition of the form `if backtesting`, `if !isLive`, `if liveMode`.
	//
	// The trailing class is what keeps `if backtest.DatasetHoldout.Spent()` out
	// of the results: there the word is a package qualifier, and a qualifier is
	// always followed by a dot. Branching on a *value* named after the mode is
	// the mistake; naming the package you are calling into is not.
	branch := regexp.MustCompile(
		`(?i)\bif\s+!?\(?\s*(is)?(backtest(ing)?|live(mode)?)\b([^.\w]|$)`)

	// Field and method shapes that mean the same thing wherever they appear,
	// including as an argument or a struct literal.
	named := []string{".IsBacktest", ".Backtesting", ".IsLive", ".LiveMode", ".BacktestMode"}

	for _, path := range allGoFiles(t) {
		if filepath.Base(path) == "architecture_test.go" {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		if match := branch.Find(content); match != nil {
			t.Errorf("%s branches on whether this is a backtest (%q). "+
				"CLAUDE.md §3.2: one code path, differing only in the source of the bars. "+
				"A mode branch makes the backtest stop describing live while still passing.",
				path, strings.TrimSpace(string(match)))
		}
		for _, pattern := range named {
			if strings.Contains(string(content), pattern) {
				t.Errorf("%s carries a %q flag. CLAUDE.md §3.2: the mode is not something "+
					"the code below the entry point is allowed to know.", path, pattern)
			}
		}
	}
}
