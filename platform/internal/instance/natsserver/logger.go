package natsserver

import (
	"fmt"
	"log/slog"
)

// slogLogger adapts the NATS server's logging interface onto the instance's
// application log.
//
// It exists because the server's failures are otherwise silent. NATS reports a
// listener it could not bind by calling Fatalf and returning, and with no
// logger installed that call does nothing at all: the start would fail with a
// timeout and no reason. Bridging the two puts the server's own account into
// the same file as the rest of the process's.
//
// Debug and trace are dropped rather than logged at a lower level. They are
// per-message protocol logging, and the platform's log is a record of what the
// process was doing, not of every frame a broker moved.
type slogLogger struct {
	log *slog.Logger
}

// Noticef records the server's routine progress: what it bound, what it named
// itself, which routes it made.
func (l slogLogger) Noticef(format string, v ...any) {
	l.log.Info(fmt.Sprintf(format, v...))
}

// Warnf records something the server thought was worth flagging.
func (l slogLogger) Warnf(format string, v ...any) {
	l.log.Warn(fmt.Sprintf(format, v...))
}

// Fatalf records an error the server could not continue past.
//
// It does not stop the process, which is what a NATS logger conventionally does
// here. The platform decides its own exits: the server stops on its own after
// this call, Start's readiness wait fails, and Run returns the error with
// everything else it has to close closed in order. Exiting from inside a log
// call would skip all of it.
func (l slogLogger) Fatalf(format string, v ...any) {
	l.log.Error(fmt.Sprintf(format, v...))
}

// Errorf records an error the server carried on past.
func (l slogLogger) Errorf(format string, v ...any) {
	l.log.Error(fmt.Sprintf(format, v...))
}

// Debugf is dropped. See the type's documentation.
func (slogLogger) Debugf(string, ...any) {}

// Tracef is dropped. See the type's documentation.
func (slogLogger) Tracef(string, ...any) {}
