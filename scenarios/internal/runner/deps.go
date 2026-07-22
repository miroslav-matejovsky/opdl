package runner

import (
	"io"
	"reflect"
	"regexp"
	"time"
)

// deps satisfies the testing.testDeps interface so MainStart can create real
// *testing.T values. Only MatchString does real work; everything else is a
// no-op. Written against Go 1.26.5.
//
// The fuzzing methods reference corpusEntry, which is unexported from the
// testing package. However, corpusEntry is a type alias to an anonymous struct,
// so we can use the same anonymous struct type in our signatures and the
// interface is satisfied structurally.
type deps struct{}

// corpusEntry mirrors testing.corpusEntry, which is a type alias (=) to this
// anonymous struct. Because it is an alias rather than a named type, the
// signatures match.
type corpusEntry = struct {
	Parent     string
	Path       string
	Data       []byte
	Values     []any
	Generation int
	IsSeed     bool
}

func (deps) MatchString(pat, str string) (bool, error)   { return regexp.MatchString(pat, str) }
func (deps) ImportPath() string                          { return "" }
func (deps) ModulePath() string                          { return "" }
func (deps) StartCPUProfile(io.Writer) error             { return nil }
func (deps) StopCPUProfile()                             {}
func (deps) WriteProfileTo(string, io.Writer, int) error { return nil }
func (deps) StartTestLog(io.Writer)                      {}
func (deps) StopTestLog() error                          { return nil }
func (deps) SetPanicOnExit0(bool)                        {}
func (deps) CheckCorpus([]any, []reflect.Type) error     { return nil }
func (deps) ResetCoverage()                              {}
func (deps) SnapshotCoverage()                           {}

func (deps) InitRuntimeCoverage() (mode string, tearDown func(string, string) (string, error), snapcov func() float64) {
	return "", nil, nil
}

func (deps) CoordinateFuzzing(time.Duration, int64, time.Duration, int64, int, []corpusEntry, []reflect.Type, string, string) error {
	return nil
}

func (deps) RunFuzzWorker(func(corpusEntry) error) error              { return nil }
func (deps) ReadCorpus(string, []reflect.Type) ([]corpusEntry, error) { return nil, nil }
