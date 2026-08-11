package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
