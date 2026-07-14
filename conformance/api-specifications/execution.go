package apispecifications

import "context"

// SkipError reports that generation could not run and was skipped rather than
// failed — for example because Kiota is not installed. The conformance command's
// test runner turns it into a skipped test; a standalone caller can treat it as a
// non-fatal warning.
type SkipError struct{ Reason string }

func (e *SkipError) Error() string { return e.Reason }

// Run regenerates the API specification artifacts: the OpenAPI specification from
// the platform's API description, then the .NET client from that specification.
// Both are always rewritten, never merely checked for staleness, so they are
// always exactly what the current source produces.
//
// The two steps run in dependency order — the client is generated from the
// specification — so the specification is written first and the client second. A
// specification failure returns before the client is touched, which stops the
// client from being regenerated against a specification that failed to write.
func Run(ctx context.Context) error {
	if err := generateOpenAPISpec(); err != nil {
		return err
	}
	return generateDotnetClient(ctx)
}
