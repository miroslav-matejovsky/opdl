package events

// Severity is how much operational attention a fact deserves. It is envelope
// metadata, not a log level: it lets an operator select the facts worth looking
// at without decoding every payload, and it never decides whether an event is
// recorded.
type Severity string

const (
	// SeverityInfo states a normal transition. It is what almost every domain
	// fact is.
	SeverityInfo Severity = "info"
	// SeverityWarn states a recoverable degradation that needs attention, such
	// as a rejected or conflicting operation.
	SeverityWarn Severity = "warn"
	// SeverityError states an operation that failed or left the process unable
	// to serve.
	SeverityError Severity = "error"
)

// DefaultSeverity is stamped on an event that does not declare its own. Most
// facts are ordinary, so declaring severity is the exception.
const DefaultSeverity = SeverityInfo

// Valid reports whether s is one of the three defined severities. The set is
// closed on purpose: a reader filters on it, so a writer cannot invent a fourth
// level that nothing knows how to rank.
func (s Severity) Valid() bool {
	switch s {
	case SeverityInfo, SeverityWarn, SeverityError:
		return true
	default:
		return false
	}
}
