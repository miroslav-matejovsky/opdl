package standby

// The two fixed instances a machine is packaged with. The names are the
// platform's, not this package's: they are the value of the manifest's
// -instance argument and the name each process reports its status under, so a
// scenario that starts a process from the manifest and then reads its status
// uses the same word for both.
const (
	primaryInstance = "primary"
	standbyInstance = "standby"
)
