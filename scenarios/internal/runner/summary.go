package runner

import (
	"flag"
	"fmt"
	"io"
	"regexp"
	"slices"
	"time"
)

func printStartSummary(w io.Writer, allSets, selectedSets []Set) {
	var runRegexp *regexp.Regexp
	if f := flag.Lookup("test.run"); f != nil && f.Value.String() != "" {
		if re, err := regexp.Compile(f.Value.String()); err == nil {
			runRegexp = re
		}
	}

	_, _ = fmt.Fprintln(w, "--- scenarios to run ---")
	for _, set := range allSets {
		inSelected := slices.ContainsFunc(selectedSets, func(s Set) bool { return s.Package == set.Package })
		for _, scenario := range set.Scenarios {
			name := set.Package + "/" + scenario.Name
			willRun := inSelected && (runRegexp == nil || runRegexp.MatchString(name))
			status := "SKIP"
			if willRun {
				status = "RUN"
			}
			_, _ = fmt.Fprintf(w, "%-4s  %s\n", status, name)
		}
	}
}

func printSummary(w io.Writer, results []*result) {
	var ran []*result
	for _, res := range results {
		if res.ran {
			ran = append(ran, res)
		}
	}
	if len(ran) == 0 {
		return
	}

	_, _ = fmt.Fprintln(w, "--- scenarios summary ---")
	for _, res := range ran {
		status := "PASS"
		if res.skipped {
			status = "SKIP"
		} else if !res.passed {
			status = "FAIL"
		}
		// Round to the nearest second, so that the summary is not too noisy.
		dur := res.duration.Round(1000 * time.Millisecond)
		_, _ = fmt.Fprintf(w, "%-4s  %s (%s)\n", status, res.name, dur)
	}
}
