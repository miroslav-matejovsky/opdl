// Package processinfo queries Windows process information.
//
// ResidentBytes reports the working set in bytes for a given process ID. It
// shells out to powershell Get-Process rather than calling a native API, and
// preserves process ID context and cancellation behavior while doing so.
package processinfo
