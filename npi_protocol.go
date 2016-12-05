package smacbase

import (
	"bytes"
	"log"
)

/* SMac NPI protocol
 *
 * OTA data:
 *   0xAE         - Start Character
 *   XX XX XX XX  - 4-byte Src/DstAddr, Little-Endian
 *   YY YY        - 2-byte Program ID, Little-Endian
 *   RR           - RSSI for received packet, 8-bit Signed Integer, 0 for Transmit Packets
 *   ZZ           - 1-byte Payload Length
 *   [payload data...]
 *   CC           - 1-byte XOR checksum
 *
 * Control data, Host -> MCU:
 *   0xBD         - Start Character
 *   XX           - 1-byte Command
 *   YY           - 1-byte Config Data Length
 *   [config data...]
 *   CC           - 1-byte XOR checksum
 *
 * Control data, MCU -> Host reply:
 *   0xBA         - Start Character
 *   XX           - 1-byte Command
 *   YY           - 1-byte Status (0 = OK)
 *   ZZ           - 1-byte Reply Data Length
 *   [reply data...]
 *   CC           - 1-byte XOR checksum
 */

// SMACNPI Control Commands
const (
	CONTROL_UNSQUELCH_HOST     = 0x00
	CONTROL_SQUELCH_HOST       = 0x01
	CONTROL_GET_RF             = 0x02
	CONTROL_SET_CENTERFREQ     = 0x03
	CONTROL_SET_TXPOWER        = 0x04
	CONTROL_SET_RF_ON          = 0x05
	CONTROL_SET_ALTERNATE_ADDR = 0x06
	CONTROL_GET_ADDRESSES      = 0x07
	CONTROL_RUN_TX             = 0x08
	CONTROL_SET_TX_TICK        = 0x09
	CONTROL_GET_IDENTIFIER     = 0x10
	CONTROL_SET_LEDS           = 0x11

	CONTROL_STATUS_OK                      = 0x00
	CONTROL_STATUS_UNKNOWN_CMD             = 0x01
	CONTROL_STATUS_MALFORMED_CTRL          = 0x02
	CONTROL_STATUS_PARAMETER_OUT_OF_BOUNDS = 0x03
	CONTROL_STATUS_FEATURE_NOT_IMPLEMENTED = 0x04
	CONTROL_STATUS_ERROR                   = 0x05
)

// Status returns an intelligible string from an otherwise cryptic uint8 NPI control frame status code
func Status(s uint8) string {
	switch s {
	case CONTROL_STATUS_OK:
		return "OK"
	case CONTROL_STATUS_UNKNOWN_CMD:
		return "UNKNOWN COMMAND"
	case CONTROL_STATUS_MALFORMED_CTRL:
		return "MALFORMED CONTROL FRAME"
	case CONTROL_STATUS_PARAMETER_OUT_OF_BOUNDS:
		return "PARAMETER OUT OF BOUNDS"
	case CONTROL_STATUS_FEATURE_NOT_IMPLEMENTED:
		return "FEATURE NOT IMPLEMENTED"
	case CONTROL_STATUS_ERROR:
		return "ERROR"
	}
	return "UNKNOWN STATUS"
}

// XorBuffer computes a checksum of a specific byte sequence
func XorBuffer(buf []byte) uint8 {
	var xor, ui uint8
	xor = 0
	for _, b := range buf {
		ui = uint8(b)
		xor = xor ^ ui
	}
	return xor
}

// NpiControl represents a command request and its reply.  To assist with synchronized wait-for-reply,
//   a Pend channel is defined to wait for the MCU's reply.
type NpiControl struct {
	Command  uint8
	Status   uint8
	Data     []byte
	Reply    []byte
	PendChan chan struct{}
}

// NewControl is the canonical way to create a new command request object
func NewControl(cmd uint8, data []uint8) *NpiControl {
	n := new(NpiControl)
	n.Command = cmd
	n.Data = data
	n.PendChan = make(chan struct{})
	return n
}

// Serialize produces a bytestream from the contents.  This is intended for 0xBD Host->MCU.
func (n *NpiControl) Serialize() []byte {
	var buf bytes.Buffer
	buf.Grow(4 + len(n.Data))
	buf.WriteByte(0xBD)
	buf.WriteByte(n.Command)
	buf.WriteByte(uint8(len(n.Data)))
	l, err := buf.Write(n.Data)
	if err != nil {
		log.Printf("NpiControl.Serialize WARNING: buf.Write(n.Data) failed with %v", err)
	}
	if l != len(n.Data) {
		log.Printf("NpiControl.Serialize WARNING: Data is %d bytes, only wrote %d", len(n.Data), l)
	}
	cksum := XorBuffer(buf.Bytes()[1:])
	buf.WriteByte(cksum)

	return buf.Bytes()
}

// Pend is a synchronization primitive; wait for the PendChan to close
func (n *NpiControl) Pend() {
	select {
	case <-n.PendChan:
		return
	}
}

// NpiRadioFrame represents an OTA frame with Address representing the
// SrcAddr if it's a received frame, and DstAddr if it's a frame-to-be-sent.
type NpiRadioFrame struct {
	Address uint32
	Program uint16
	Rssi    int8
	Data    []byte
}

// NewRadioFrame is the canonical way to create a new SMac packet
func NewRadioFrame(addr uint32, prog uint16, data []byte) *NpiRadioFrame {
	n := new(NpiRadioFrame)
	n.Address = addr
	n.Program = prog
	n.Rssi = 0
	n.Data = data
	return n
}

// Serialize produces a bytestream for the radio frame in question
func (n *NpiRadioFrame) Serialize() []byte {
	var buf bytes.Buffer
	buf.Grow(9 + len(n.Data))
	buf.WriteByte(0xAE)
	buf.WriteByte(uint8(n.Address & 0xFF))
	buf.WriteByte(uint8((n.Address >> 8) & 0xFF))
	buf.WriteByte(uint8((n.Address >> 16) & 0xFF))
	buf.WriteByte(uint8((n.Address >> 24) & 0xFF))
	buf.WriteByte(uint8(n.Program & 0xFF))
	buf.WriteByte(uint8(n.Program >> 8))
	buf.WriteByte(0) // RSSI field is empty for transmit packets
	buf.WriteByte(uint8(len(n.Data)))
	l, err := buf.Write(n.Data)
	if err != nil {
		log.Printf("NpiRadioFrame.Serialize WARNING: buf.Write(n.Data) failed with %v", err)
	}
	if l != len(n.Data) {
		log.Printf("NpiRadioFrame.Serialize WARNING: Data is %d bytes, only wrote %d", len(n.Data), l)
	}
	cksum := XorBuffer(buf.Bytes()[1:])
	buf.WriteByte(cksum)

	return buf.Bytes()
}
