// Package operations records local operational events for platform operators.
//
// These events deliberately do not depend on the Event Fabric. Connection loss,
// journal unavailability, and projection failure are precisely the conditions an
// operator needs to observe, so every event is written as one JSON object to the
// process error stream. A configured recorder also appends the same records to a
// local JSONL file for retention and automated scenario diagnostics.
package operations
