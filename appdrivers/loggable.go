package appdrivers

import (
	"fmt"
	"github.com/spirilis/smacbase"
)

/* loggable.go defines the LogText interface, whose only method Log() (printf-style arguments) logs text to
 * some sort of log output mechanism.  Typically STDOUT.
 */

// LogText receives a printf-style specifier and logs it somewhere.
type LogText interface {
	Printf(string, ...interface{})
}

// GenericStdout is a LogText implementation that displays text on STDOUT.
type GenericStdout struct{}

// Printf implements the LogText interface
func (g GenericStdout) Printf(f string, v ...interface{}) {
	fmt.Printf(f, v...)
}

// FrameStdout is a generic type for printing received packets
type FrameStdout struct {
	Logger LogText
}

// Receive implements smacbase.FrameReceiver
func (f *FrameStdout) Receive(l *smacbase.LinkMgr, rssi int8, srcAddr uint32, progID uint16, payload []byte) bool {
	outStr := fmt.Sprintf("RX: %08X Prog = %04X, payload = [", srcAddr, progID)
	for _, b := range payload {
		outStr += fmt.Sprintf("%02X ", b)
	}
	outStr += fmt.Sprintf("], RSSI=%d\n", rssi)
	f.Logger.Printf(outStr)
	return true
}
