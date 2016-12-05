package smacbase

import (
	"github.com/jacobsa/go-serial/serial"
	"io"
	"log"
)

// npi_phy.go - Define the serial I/O NPI connection and manage NPI frames

// NewSerialPHY - Open the specified serial port
// TODO: Implement RTS/CTS control lines
func NewSerialPHY(path string, baud uint) (io.ReadWriteCloser, error) {
	opts := serial.OpenOptions{
		PortName:              path,
		BaudRate:              baud,
		DataBits:              8,
		StopBits:              1,
		ParityMode:            serial.PARITY_NONE,
		InterCharacterTimeout: 0,
		MinimumReadSize:       1,
	}

	return serial.Open(opts)
}

// RunNPI is the meat of this application - Handle the serial I/O and marshalling of SMac radio frames to/fro the MCU
// As the RunNPI framework uses an io.ReadWriteCloser for its PHY, it's a flexible subsystem that can use many different
// interfaces for its I/O, including software test harnesses that satisfy the io.ReadWriteCloser interface.
func RunNPI(phy io.ReadWriteCloser, frameXmit chan *NpiRadioFrame, frameRecv chan *NpiRadioFrame, ctrlXmit chan *NpiControl, reportFaulted chan struct{}) {
	// control chan for passing PHY-dead or halt info back and forth with this func
	childErrRpt := reportFaulted

	// chan for receiving Control frames from npiPhyReader; we get in the middle of this so flow-control control frames
	// can be intercepted and processed by RunNPI without requiring external intervention
	ctrlReplies := make(chan NpiControl, 4)
	ctrlWrites := make(chan *NpiControl, 4)

	// chan for notifying writer when output needs to be halted (true) or not (false)
	squelchWrites := make(chan bool)

	// Keeping track of externally-initiated control frames so we can stuff their Reply and close their PendChan
	var ctrlRegistry map[uint8]*NpiControl
	ctrlRegistry = make(map[uint8]*NpiControl)

	// Launch goroutines for npiPhyReader and npiPhyWriter
	go npiPhyReader(phy, frameRecv, ctrlReplies, childErrRpt)
	go npiPhyWriter(phy, squelchWrites, frameXmit, ctrlWrites, childErrRpt)

	defer phy.Close()

	// Main loop with select block running the show
	for {
		select {
		case <-childErrRpt:
			return
		case rep := <-ctrlReplies:
			// Handle internally-sourced control frame replies, such as MCU->Host flow control
			if rep.Command == CONTROL_SQUELCH_HOST && rep.Status == CONTROL_STATUS_OK {
				squelchWrites <- true // Tell npiPhyWriter to quit servicing writes
				continue
			}
			if rep.Command == CONTROL_UNSQUELCH_HOST && rep.Status == CONTROL_STATUS_OK {
				squelchWrites <- false // Tell npiPhyWriter it's clear to write again
				continue
			}

			// Finally: Check if the control frame reply came from an external request we're tracking
			if ctrlRegistry[rep.Command] != nil {
				n := ctrlRegistry[rep.Command]
				n.Status = rep.Status
				n.Reply = rep.Reply
				select {
				case <-n.PendChan: // do nothing if PendChan is already closed
				default:
					close(n.PendChan) // Notify external function that a reply was received for this control cmd
				}
				ctrlRegistry[rep.Command] = nil // forget this one now
			}
		case n := <-ctrlXmit:
			ctrlRegistry[n.Command] = n
			ctrlWrites <- n
		}
	}
}

// npiPhyReader has the distinguished displeasure of processing every byte coming in from the serial port to parse
// valid frames out of it, keeping in mind that individual sequences of read bytes might not contain the whole frame
// or contains parts of the next frame, possibly invalid frames due to invalid checksum, etc.
func npiPhyReader(phy io.ReadWriteCloser, outFrame chan<- *NpiRadioFrame, ctrlReply chan NpiControl, halt chan struct{}) {
	var serbuf, serbufBacking, frame []byte
	serbufBacking = make([]byte, 65536)
	frame = make([]byte, 256)
	var framePos, payloadLen int

	for {
		// We need to use serbufBacking because serbuf's start position is incremented in a long loop, thus losing
		// its perspective of where "position 0" actually lives.
		serbuf = serbufBacking[0:65536]
		l, err := phy.Read(serbuf)
		if err != nil {
			select {
			case <-halt: // can't close an already-closed channel
			default:
				close(halt) // Notify parent that something is wrong with the PHY
			}
			return
		}
		//log.Printf("npiPhyReader: Read %d", l)
		serbuf = serbuf[:l]
		// Process the contents
		var ui uint8
		for len(serbuf) > 0 {
			ui = uint8(serbuf[0])
			if framePos == 0 { // Search for a valid StartChar
				if ui == 0xAE || ui == 0xBA {
					frame[0] = ui
					framePos = 1
					/* advance serbuf and loop back around; if bytes remain in serbuf, the
					 * next if block will do useful things
					 */
					serbuf = serbuf[1:]
					//log.Printf("npiPhyReader: Found StartChar=%2x", uint8(frame[0]))
					continue
				}
			}
			if framePos > 0 { // StartChar found; search for payloadLen
				if payloadLen == 0 && (frame[0] == 0xAE && framePos == 8) {
					payloadLen = 10 + int(ui)
					//log.Printf("npiPhyReader: SC=%2x, dataLen=%d, payloadLen=%d", uint8(frame[0]), ui, payloadLen)
				}
				if payloadLen == 0 && (frame[0] == 0xBA && framePos == 3) {
					payloadLen = 5 + int(ui)
					//log.Printf("npiPhyReader: SC=%2x, dataLen=%d, payloadLen=%d", uint8(frame[0]), ui, payloadLen)
				}
				frame[framePos] = ui
				framePos++
			}
			if payloadLen > 0 && framePos == payloadLen {
				// Completed frame; verify checksum and send it on its way
				frame = frame[:framePos]
				cksum := XorBuffer(frame[1 : len(frame)-1])
				//log.Printf("npiPhyReader: Frame completed, cksum=%2x, frame[%d]=%2x, len(frame)=%d", cksum, len(frame)-1, uint8(frame[len(frame)-1]), len(frame))
				if uint8(frame[len(frame)-1]) == cksum {
					// Valid frame; process
					if frame[0] == 0xAE { // OTA recv radio frame
						n := new(NpiRadioFrame)
						var addr uint32
						addr = uint32(frame[1])
						addr |= uint32(frame[2]) << 8
						addr |= uint32(frame[3]) << 16
						addr |= uint32(frame[4]) << 24
						var progID uint16
						progID = uint16(frame[5])
						progID |= uint16(frame[6]) << 8
						var rssi int8
						rssi = int8(frame[7])
						var dataLen uint8
						var payload, payloadCp []byte
						dataLen = uint8(frame[8])
						payload = frame[9 : 9+dataLen]
						payloadCp = make([]byte, dataLen)
						copy(payloadCp, payload) // Make a copy to avoid overloading []frame space

						n.Address = addr
						n.Program = progID
						n.Data = payloadCp
						n.Rssi = rssi
						outFrame <- n // send newly parsed packet on its way
					}
					if frame[0] == 0xBA { // Control cmd reply
						replData := make([]byte, uint8(frame[3]))
						copy(replData, frame[4:4+uint8(frame[3])])
						ctlFrame := NpiControl{
							Command: uint8(frame[1]),
							Status:  uint8(frame[2]),
							Reply:   replData,
						}
						ctrlReply <- ctlFrame
					}
				} // Else Checksum failed; ignore the whole frame
				// Reset []frame buffer
				frame = frame[0:256]
				framePos = 0
				payloadLen = 0
			}
			serbuf = serbuf[1:]
		}
	}
}

// npiPhyWriter is a bit simpler than npiPhyReader, in that it just dumps data to the serial port.
// The squelch feature is a neat one but it could lead to deadlocks if used without care.
func npiPhyWriter(phy io.ReadWriteCloser, squelch <-chan bool,
	frameXmit <-chan *NpiRadioFrame, ctrlXmit <-chan *NpiControl,
	halt chan struct{}) {
	var buf []byte
	var xmitHalted bool
	xmitHalted = false
	for {
		select {
		case <-halt:
			return
		case s := <-squelch:
			xmitHalted = s
			log.Printf("npiPhyWriter: xmitHalted=%v", xmitHalted)
			for xmitHalted == true {
				// While npiPhyWriter is squelched, ignore all channels except the squelch channel and
				// the RunNPI halt request.
				select {
				case <-halt:
					return
				case s := <-squelch:
					xmitHalted = s
					log.Printf("npiPhyWriter: xmitHalted=%v", xmitHalted)
				}
			}
		case otaFrame := <-frameXmit:
			buf = otaFrame.Serialize()
			_, err := phy.Write(buf)
			if err != nil {
				select {
				case <-halt: // can't close an already-closed channel
				default:
					close(halt) // Notify parent that something is wrong with the PHY
				}
				return
			}
			//log.Printf("npiPhyWriter: Committed an OTA frame of writeLen=%d, dstAddr=%08x, program ID=%04x", w, otaFrame.Address, otaFrame.Program)
		case ctlFrame := <-ctrlXmit:
			buf = ctlFrame.Serialize()
			_, err := phy.Write(buf)
			if err != nil {
				select {
				case <-halt: // can't close an already-closed channel
				default:
					close(halt) // Notify parent that something is wrong with the PHY
				}
				return
			}
			//log.Printf("npiPhyWriter: Committed a Ctrl frame of writeLen=%d, Command=%02x", w, ctlFrame.Command)
		}
	}
}
