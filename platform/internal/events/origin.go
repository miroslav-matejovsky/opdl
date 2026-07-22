package events

import "fmt"

// Origin is the identity of the platform process that stated a fact: which
// process, of which machine, of which deployment.
//
// It is constant for a whole process run and is stamped onto every envelope.
// The shared site journal pools events from several machines and from more than
// one process per machine, so identity travels with each fact rather than
// living in a file name a reader may never see.
//
// The deployment fields down to MachineProfile are domain identity: they name
// the machine the fact is about. ProcessRole and PID are operational identity:
// they name which of that machine's processes wrote it. The distinction
// matters. A machine may run a primary and a standby process, and domain
// behavior stays machine-scoped, so registration counts one voter per machine
// no matter how many processes it runs. Domain code reads Machine; only an
// operator troubleshooting a specific process reads ProcessRole and PID.
type Origin struct {
	// Project is the deployment project identifier.
	Project string `json:"project"`
	// Environment is the deployment environment name.
	Environment string `json:"environment"`
	// Site is the deployment site identifier.
	Site string `json:"site"`
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

// Validate reports whether o names a complete origin. Every field is required:
// a journal that pools the events of a whole site cannot attribute a fact whose
// origin is only partly stated, and a partly stated origin is always a
// composition bug rather than something a reader can repair later.
func (o Origin) Validate() error {
	missing := ""
	switch {
	case o.Project == "":
		missing = "project"
	case o.Environment == "":
		missing = "environment"
	case o.Site == "":
		missing = "site"
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
