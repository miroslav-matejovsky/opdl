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
	"github.com/miroslav-matejovsky/opdl/scenarios/nats"
	"github.com/miroslav-matejovsky/opdl/scenarios/registration"
	"github.com/miroslav-matejovsky/opdl/scenarios/resilience"
	"github.com/miroslav-matejovsky/opdl/scenarios/sdk"
	"github.com/miroslav-matejovsky/opdl/scenarios/standby"
)

func main() {
	// testing.Init registers the test.* flags the runner reads. It registers them
	// wherever flag.CommandLine points, so pointing that at a private set for the
	// call keeps three dozen implementation details out of this command's -h and
	// out of its accepted arguments. The flag values themselves are variables
	// inside the testing package, so setting them through this set is what the
	// runner sees.
	testFlags := flag.NewFlagSet("scenarios-testing", flag.ContinueOnError)
	commandLine := flag.CommandLine
	flag.CommandLine = testFlags
	testing.Init()
	flag.CommandLine = commandLine

	parallel := flag.Int("parallel", runtime.GOMAXPROCS(0), "maximum scenarios to run at once")
	timeout := flag.Duration("timeout", 30*time.Minute, "overall timeout for the whole run")
	run := flag.String("run", "", "run only scenarios whose name matches this regexp")
	count := flag.Int("count", 1, "run each scenario n times; scenarios never use cached results")
	verbose := flag.Bool("v", false, "report each scenario as it runs")
	flag.Parse()

	// Translate this command's flags into the test.* flags the runner reads.
	setTestFlag(testFlags, "test.parallel", strconv.Itoa(*parallel))
	setTestFlag(testFlags, "test.timeout", timeout.String())
	setTestFlag(testFlags, "test.run", *run)
	setTestFlag(testFlags, "test.count", strconv.Itoa(*count))
	if *verbose {
		setTestFlag(testFlags, "test.v", "true")
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

// scenarioSets returns every category's scenarios. This list is the suite: a
// category that is not here does not run, and adding one is an import and a
// line.
//
// The order is the default run order, which matters only for the scenarios that
// do not call t.Parallel. It runs from the smallest deployment outwards, so a
// broken build fails on the smoke scenario rather than partway through a
// four-machine one.
func scenarioSets() []runner.Set {
	return []runner.Set{
		registration.Scenarios(),
		nats.Scenarios(),
		resilience.Scenarios(),
		standby.Scenarios(),
		sdk.Scenarios(),
	}
}

// setTestFlag sets one of the runner's test.* flags. A flag that is not present
// is ignored: the set is the standard library's and its contents move between Go
// releases, and a missing knob must not stop a run.
func setTestFlag(set *flag.FlagSet, name, value string) {
	if f := set.Lookup(name); f != nil {
		_ = f.Value.Set(value)
	}
}
