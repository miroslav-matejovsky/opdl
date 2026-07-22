package scenarios

import (
	"flag"
	"fmt"
	"os"
	"testing"
)

// TestMain skips the whole black-box scenario suite under -short, so the unit
// gate compiles every scenario without running any. The scenarios build and run
// the platform, which is far too heavy for -short; they run in the scenarios
// command and, until stage 05 categorizes them, here without -short.
func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		fmt.Println("skipping scenario suite in -short mode")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
