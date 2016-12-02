package appdrivers

import (
	"github.com/spirilis/smacbase"
	"log"
)

/* deviceid is responsible for receiving Device ID registrations (ProgID=0x2000) and
 * storing them for later lookup by other applications.
 */

// DeviceIdRegistration is passed to other DeviceID-aware objects for lookup purposes
type DeviceIdRegistration struct {
	Registrations map[uint16]string
}

// NewDeviceIdRegistration is the canonical way to create a DeviceIdRegistration and bind it to a Link.
func NewDeviceIdRegistration(l *smacbase.LinkMgr) *DeviceIdRegistration {
	d := new(DeviceIdRegistration)
	d.Registrations = make(map[uint16]string)
	l.RegisterProgramHandler(0x2000, d)
	return d
}

// Receive implements smacbase.FrameReceiver
func (d *DeviceIdRegistration) Receive(l *smacbase.LinkMgr, srcAddr uint32, progID uint16, payload []byte) bool {
	if progID != 0x2000 {
		log.Printf("DeviceIdRegistration.Receive: received an invalid frame with progID=%04X, expected 0x2000", progID)
		return true // Error, not intended for us?
	}
	if len(payload) < 2 {
		log.Printf("DeviceIdRegistration.Receive: received a frame with payload size < 2, invalid packet")
		return false // bad packet, stop processing it
	}
	var deviceID uint16
	var deviceDescription string

	deviceID = uint16(payload[0]) | (uint16(payload[1]) << 8)
	deviceDescription = string(payload[2:])

	d.Registrations[deviceID] = deviceDescription
	return false
}

// GetByDevice is used by other appdrivers and implements QueryDevice
func (d *DeviceIdRegistration) GetByDevice(devID uint16) (interface{}, error) {
	return d.Registrations[devID], nil
}
