package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The runbooks, checked as commands rather than as prose.
//
// docs/mobile.md and deploy/README.md are procedures somebody follows on the
// VPS by pasting. That makes a wrong command in them a defect with the same
// shape as a wrong line of code, except that nothing compiles it — the first
// person to find out is the operator, halfway through a procedure, with the
// stack in whatever state the previous step left it.
//
// These tests only check the mechanical parts: whether a command can run at
// all on the host it is written for. Whether it does the right thing is still
// a human's job.

// runbooks are the documents that describe commands to run on the VPS.
func runbooks() []string {
	return []string{
		filepath.Join("..", "docs", "mobile.md"),
		filepath.Join("..", "deploy", "README.md"),
	}
}

// fencedBlocks returns the line indexes inside each ``` fenced block.
func fencedBlocks(lines []string) [][]int {
	var blocks [][]int
	var current []int
	inside := false

	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inside {
				blocks = append(blocks, current)
				current = nil
			}
			inside = !inside
			continue
		}
		if inside {
			current = append(current, i)
		}
	}
	return blocks
}

/*
TestTheRunbooksNeverAskTheVPSForAPsqlItDoesNotHave.

# What this prevents

The VPS runs Docker and Tailscale and nothing else. It has no psql client, and
never will — installing one to satisfy a documentation snippet would be putting
a database console on a host to work around a command that could have gone
through the container in the first place.

Every "psql: command not found" in this project has come from a runbook line
written on a laptop, where psql exists. The C3 delivery procedure had two of
them: the first was found by an operator mid-procedure, and the second was
still sitting four checks later, in a step nobody had reached yet.

So a bare psql command is a defect unless its block is explicitly the local
half of an SSH tunnel. Anything else goes through `make psql`, which runs the
client inside the container that already has one.
*/
func TestTheRunbooksNeverAskTheVPSForAPsqlItDoesNotHave(t *testing.T) {
	for _, doc := range runbooks() {
		raw, err := os.ReadFile(doc)
		if err != nil {
			t.Skipf("no %s alongside the module: %v", doc, err)
		}
		lines := strings.Split(string(raw), "\n")

		for _, block := range fencedBlocks(lines) {
			// A block that opens an SSH tunnel is describing the reader's own
			// machine, which may have a client of its own.
			tunnel := false
			for _, i := range block {
				if strings.Contains(lines[i], "ssh -L") {
					tunnel = true
				}
			}
			if tunnel {
				continue
			}

			for _, i := range block {
				command := strings.TrimPrefix(strings.TrimSpace(lines[i]), "$ ")
				if command != "psql" && !strings.HasPrefix(command, "psql ") {
					continue
				}
				// A continuation of the line above, which is where the thing
				// that put it in a container would be.
				if i > 0 && strings.HasSuffix(strings.TrimRight(lines[i-1], " \t"), `\`) {
					continue
				}
				t.Errorf("%s:%d runs psql on the host, which has no client:\n\t%s\n"+
					"use `make psql`, which runs one in the postgres container",
					doc, i+1, strings.TrimSpace(lines[i]))
			}
		}
	}
}

/*
TestTheRunbooksNameTheDatabaseByServiceRatherThanByContainer.

# What this prevents

`btcusd-trading-platform-postgres-1` is a name docker composes from the project
and the service. podman composes a different one, and `make engine` exists
precisely because either may be the engine on a given host. A command naming
the container works until it does not, and then reports that the container does
not exist — which reads as a stack that is down rather than a command that is
wrong.

The service name is the stable half, so compose is what should be doing the
lookup.
*/
func TestTheRunbooksNameTheDatabaseByServiceRatherThanByContainer(t *testing.T) {
	for _, doc := range runbooks() {
		raw, err := os.ReadFile(doc)
		if err != nil {
			t.Skipf("no %s alongside the module: %v", doc, err)
		}

		for i, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, "btcusd-trading-platform-postgres-1") {
				t.Errorf("%s:%d names the container the engine happened to create:\n\t%s\n"+
					"ask compose for the service instead — podman names it differently",
					doc, i+1, strings.TrimSpace(line))
			}
		}
	}
}
