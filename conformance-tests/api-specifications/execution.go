package apispecifications

import "context"

// SkipError reports that generation could not run and was skipped rather than
// failed — for example because Kiota is not installed. The conformance command's
// test runner turns it into a skipped test; a standalone caller can treat it as a
// non-fatal warning.
type SkipError struct{ Reason string }

func (e *SkipError) Error() string { return e.Reason }

// Run regenerates the API specification artifacts: the OpenAPI specification and
// its Markdown companion from the platform's API description, then the .NET client
// from the OpenAPI specification. All are always rewritten, never merely checked
// for staleness, so they are always exactly what the current source produces.
//
// The steps run in dependency order — the markdown and the client are generated
// from the specification — so the specification is written first, the markdown
// second, and the client third. A specification or markdown failure returns before
// the client is touched, which stops the client from being regenerated against a
// specification that failed to write.
func Run(ctx context.Context) error {
	yamlBytes, err := generateOpenAPISpec()
	if err != nil {
		return err
	}
	if err := generateOpenAPIMarkdown(yamlBytes); err != nil {
		return err
	}
	return generateDotnetClient(ctx)
}
