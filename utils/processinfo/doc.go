// Package processinfo queries operating-system process information across platforms.
//
// ResidentBytes reports resident memory usage in bytes for a given process ID.
// Where native operating-system APIs are not directly available, portable command
// fallbacks (such as powershell on Windows or ps on Unix platforms without /proc)
// are invoked while preserving process ID context and cancellation behavior.
package processinfo
