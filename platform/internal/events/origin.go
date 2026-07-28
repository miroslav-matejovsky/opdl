package events

import "fmt"

// Origin identifies the process that stated a fact. Machine and MachineProfile
// are domain identity. ProcessRole and PID are operational identity and do not
// turn primary and standby processes into separate domain voters.
type Origin struct {
	// Machine is the deployment machine identifier.
	Machine string `json:"machine"`
	// MachineProfile is the machine's purpose, such as "sensor-node".
	MachineProfile string `json:"machine_profile"`
	// ProcessRole is the local role of the writing process, such as primary or
	// standby. It is operational identity only and never a domain identity.
	ProcessRole string `json:"process_role"`
	// PID is the operating system process identifier of the writing process. It
	// is what distinguishes two runs of the same role on one machine.
	PID int `json:"pid"`
}

// Validate reports whether o names a complete origin.
func (o Origin) Validate() error {
	missing := ""
	switch {
	case o.Machine == "":
		missing = "machine"
	case o.MachineProfile == "":
		missing = "machine_profile"
	case o.ProcessRole == "":
		missing = "process_role"
	}
	if missing != "" {
		return fmt.Errorf("origin %s is required", missing)
	}
	if o.PID <= 0 {
		return fmt.Errorf("origin pid must be positive, got %d", o.PID)
	}
	return nil
}
