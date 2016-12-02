package appdrivers

import (
	"fmt"
	"spirilis/smacbase"
)

// ThermocoupleStdout is an SMac handler that receives temperature data, and relays it directly to stdout.  Duh.
type ThermocoupleStdout struct {
	Link      *smacbase.LinkMgr
	SeenNodes map[uint16]int16 // Map of logical device IDs and last seen thermocouple value
}

// NewThermocoupleStdout creates a new instance and attaches it to the link.
func NewThermocoupleStdout(l *smacbase.LinkMgr) *ThermocoupleStdout {
	ts := new(ThermocoupleStdout)
	ts.Link = l
	ts.SeenNodes = make(map[uint16]int16)

	l.RegisterProgramHandler(0x2001, ts)
	return ts
}

// Receive implements smacbase.FrameReceiver - returns true if LinkMgr should continue parsing after this
func (ts *ThermocoupleStdout) Receive(l *smacbase.LinkMgr, srcAddr uint32, progID uint16, payload []byte) bool {
	// Extract thermocouple data
	if progID != 0x2001 {
		return true // apparently this packet wasn't intended for us, so, continue processing
	}
	if len(payload) != 7 {
		return false // stop processing further, as this packet is malformed.
	}
	var tmp, devid uint16 // Using a uint16 temporary to avoid mangling conversion with sign-extends
	var tc, amb int16
	devid = uint16(payload[0]) | (uint16(payload[1]) << 8)
	tmp = uint16(payload[2]) | (uint16(payload[3]) << 8)
	tc = int16(tmp)
	tmp = uint16(payload[4]) | (uint16(payload[5]) << 8)
	amb = int16(tmp)

	ts.SeenNodes[devid] = tc

	fmt.Printf("Device ID %04X: TC = %d Celsius, Ambient = %d Celsius (srcAddr = %08X)\n", devid, tc, amb, srcAddr)
	return true // continue processing as there may be other intelligent apps using it
}
