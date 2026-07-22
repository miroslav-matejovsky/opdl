// Command scenarios runs the black-box scenario suite: it builds deployment
// packages and runs the resulting platform binaries as subprocesses, driving
// them from outside the way a customer would.
//
// It runs the scenarios through the standard library test runner, so each
// scenario keeps t.Parallel, t.Cleanup, t.Run, and require. Parallelism,
// timeout, and selection are this command's flags rather than the taskfile's.
//
// Usage:
//
//	go run ./cmd [flags]
//
// Flags:
//
//	-parallel n   maximum scenarios to run at once (default GOMAXPROCS)
//	-timeout d    overall timeout for the whole run (default 30m)
//	-run regexp   run only scenarios whose name matches regexp
//	-count n      run each scenario n times (default 1; scenarios never cache)
//	-v            report each scenario as it runs
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"testing"
	"time"

	"github.com/miroslav-matejovsky/opdl/scenarios/internal/runner"
)

func main() {
	// testing.Init registers the test.* flags RunTests reads onto
	// flag.CommandLine. It must run before the friendly flags are parsed.
	testing.Init()

	parallel := flag.Int("parallel", runtime.GOMAXPROCS(0), "maximum scenarios to run at once")
	timeout := flag.Duration("timeout", 30*time.Minute, "overall timeout for the whole run")
	run := flag.String("run", "", "run only scenarios whose name matches this regexp")
	count := flag.Int("count", 1, "run each scenario n times; scenarios never use cached results")
	verbose := flag.Bool("v", false, "report each scenario as it runs")
	flag.Parse()

	// Translate the friendly flags into the test.* flags the runner reads.
	setTestFlag("test.parallel", strconv.Itoa(*parallel))
	setTestFlag("test.timeout", timeout.String())
	setTestFlag("test.run", *run)
	setTestFlag("test.count", strconv.Itoa(*count))
	if *verbose {
		setTestFlag("test.v", "true")
	}

	// Backstop timeout. RunTests honours test.timeout as a per-test deadline but
	// cannot interrupt a goroutine stuck in a syscall or waiting on a child
	// process, which a scenario driving external processes can produce. This kills
	// the whole run past the deadline; the job objects procrun places children in
	// take them down with it. On a normal finish the process exits first and this
	// never fires.
	go func() {
		time.Sleep(*timeout + time.Minute)
		fmt.Fprintln(os.Stderr, "scenarios: hard timeout exceeded; dumping goroutines")
		_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 2)
		os.Exit(2)
	}()

	os.Exit(runner.Run(scenarioSets()))
}

// scenarioSets returns the scenarios to run. Stage 05 wires the category
// packages (registration, nats, resilience, standby, sdk) in here; until then
// the set is empty and the command proves only the plumbing.
func scenarioSets() []runner.Set {
	return []runner.Set{{
		Package: "selfcheck",
		Scenarios: []runner.Scenario{
			{Name: "AlphaParallel", Func: func(t *testing.T) { t.Parallel() }},
			{Name: "BetaPlain", Func: func(t *testing.T) {}},
			{Name: "GammaFails", Func: func(t *testing.T) { t.Fatal("intentional") }},
		},
	}}
}

// setTestFlag sets a registered flag by name, ignoring one that is not present.
func setTestFlag(name, value string) {
	if f := flag.Lookup(name); f != nil {
		_ = f.Value.Set(value)
	}
}
