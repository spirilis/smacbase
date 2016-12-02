package smacbase

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

/*
 * The LinkMgr is a management layer atop the NPI PHY.  LinkMgr passes along NPI OTA frames but also provides a high-level
 * API to controlling the radio link, wrapping the underlying Control Frames with API methods.
 *
 * The LinkMgr is also become the central broker for divvying out received RF frames to a registry of handlers.
 *
 * API:
 *
 * NewLinkMgr(phyPath, baudRate) (*LinkMgr, error) - Starts the PHY and LinkMgr goroutines; live NPI link is available upon successful return
 * *LinkMgr.Send(addr, progID, data) error - Submit an OTA frame (error only if PHY died)
 * *LinkMgr.RegisterProgramHandler(progID, handler) - Register a handler (object implementing FrameReceiver) to process RX frames with progID
 * *LinkMgr.RegisterAddressHandler(addr, handler) - Register a handler to process RX frames coming from a specific IEEE address
 * *LinkMgr.RegisterAllHandler(handler) - Add a handler to the "Firehose", which sees all frames unless a previous handler returned false (i.e. "do not process further")
 * *LinkMgr.Ctrl(cmd uint8, data []byte) (status uint8, reply []byte, error) - Send a Control frame and wait for reply, returned as status & reply data.
 *
 * *LinkMgr.DeregisterHandler(handler) - Remove the specified handler from ALL handler registries, including firehose (only way to remove one from firehose)
 * *LinkMgr.DeregisterProgramHandler(progID) - Remove the handler for a specific progID
 * *LinkMgr.DeregisterAddressHandler(addr) - Remove the handler for a specific IEEE address
 *
 * High-level Control API:
 * *LinkMgr.GetRadio() (bool, uint32, int8, uint16) - Returns RX ON/OFF, Center Frequency, TXpower (dBm), Auto-TX tick interval (ms)
 * *LinkMgr.GetAddresses() (uint32, uint32) - Returns IEEE address, Alternate address (or 0 if not set)
 * *LinkMgr.GetIdentifier() (string) - Returns the NPI microcontroller's compiled ID string
 * *LinkMgr.SetAlternateAddress(uint32) - Sets the secondary radio address, or disables it if 0
 * *LinkMgr.SetFrequency(uint32) (error) - Sets the RF center frequency
 * *LinkMgr.SetPower(int8) (error) - Sets the TX power in dBm (supported values -10, 0-12, 14 if NPI firmware compiled with CCFG_FORCE_VDDR_HH=1)
 * *LinkMgr.SetTxInterval(uint16) - Sets the interval (in milliseconds) between automatic ticks of the TX request, or disables it with 0
 * *LinkMgr.RunTx() - Manually trigger a TX if any frames are waiting in the TX queue
 * *LinkMgr.On(bool) - Switch RX on/off
 *
 * ^ All these control API functions have an additional (error) argument at the end of their reply set, or if there is no reply set listed, it's the only argument.
 *   This will inform the user if the NPI PHY faulted or if there was a non-OK status code returned by the NPI microcontroller.
 */

// LinkMgr - central type surrounding the SMac NPI link manager
type LinkMgr struct {
	Phy io.ReadWriteCloser

	FrameTX chan *NpiRadioFrame
	FrameRX chan *NpiRadioFrame
	CtrlTX  chan *NpiControl
	NpiDied chan struct{}

	// Registry of RX frame receivers
	registryMutex     sync.Mutex
	RxRegistryProgram map[uint16]FrameReceiver
	RxRegistryAddress map[uint32]FrameReceiver
	RxFirehose        []FrameReceiver // All frames process through this list after the Program, Address-specific handlers have run
}

// FrameReceiver is an interface used to handle incoming RX frames.
type FrameReceiver interface {
	// Receive is called automatically by the LinkMgr with a pointer to the LinkMgr (for sending frames or controlling the link),
	// the SrcAddr, ProgramID, data payload, and the implementation should return a bool for whether the LinkMgr should stop processing
	// the frame here or continue passing it to other handlers.
	Receive(*LinkMgr, uint32, uint16, []byte) bool
}

// NewLinkMgr gets the ball rolling and starts the PHY in a goroutine (RunNPI), along with its RX manager
func NewLinkMgr(phyPath string, baudRate uint) (*LinkMgr, error) {
	phy, err := NewSerialPHY(phyPath, baudRate)
	if err != nil {
		return nil, errors.New("NewLinkMgr error creating PHY: " + err.Error())
	}

	l := new(LinkMgr)
	l.FrameTX = make(chan *NpiRadioFrame)
	l.FrameRX = make(chan *NpiRadioFrame)
	l.CtrlTX = make(chan *NpiControl)
	l.NpiDied = make(chan struct{})
	l.Phy = phy

	l.RxRegistryProgram = make(map[uint16]FrameReceiver)
	l.RxRegistryAddress = make(map[uint32]FrameReceiver)

	go RunNPI(phy, l.FrameTX, l.FrameRX, l.CtrlTX, l.NpiDied)
	// Launch a goroutine which dispatches received RX frames
	err = l.ExecRxHandler()
	if err != nil {
		return nil, errors.New("NewLinkMgr error starting RX Handler: " + err.Error())
	}
	return l, nil
}

// Close will stop the NPI link
func (l *LinkMgr) Close() error {
	select {
	case <-l.NpiDied:
		return errors.New("NPI PHY link already down")
	default:
	}
	close(l.NpiDied)
	return nil
}

// Send is used by clients to transmit a radio frame over the air
func (l *LinkMgr) Send(dstAddr uint32, program uint16, data []byte) error {
	// Do a quick select to see if l.NpiDied was closed
	select {
	case <-l.NpiDied:
		return errors.New("NPI PHY link faulted")
	default:
	}
	// Send a new frame to the SMac NPI microcontroller
	radioFrame := NewRadioFrame(dstAddr, program, data)
	l.FrameTX <- radioFrame
	return nil
}

// CtrlTimeout is an error denoting timeout in Ctrl()
type CtrlTimeout string

func (c CtrlTimeout) Error() string { return string(c) }

// Ctrl submits a control frame to the NPI microcontroller, then returns the (status, return data) reply.
func (l *LinkMgr) Ctrl(cmd uint8, data []byte) (uint8, []byte, error) {
	// Do a quick select to see if l.NpiDied was closed
	select {
	case <-l.NpiDied:
		return cmd, nil, errors.New("NPI PHY link faulted")
	default:
	}

	cmdFrame := NewControl(cmd, data)
	l.CtrlTX <- cmdFrame
	tck := time.After(time.Second * 3)
	select {
	case <-l.NpiDied:
		return cmd, nil, errors.New("NPI PHY link faulted")
	case <-cmdFrame.PendChan:
		return cmdFrame.Status, cmdFrame.Reply, nil
	case <-tck:
		// Timeout
		return cmd, nil, CtrlTimeout("Ctrl TIMEOUT")
	}
}

// CtrlForget sends a control frame and returns immediately, ignoring the results
func (l *LinkMgr) CtrlForget(cmd uint8, data []byte) error {
	// Do a quick select to see if l.NpiDied was closed
	select {
	case <-l.NpiDied:
		return errors.New("NPI PHY link faulted")
	default:
	}

	cmdFrame := NewControl(cmd, data)
	l.CtrlTX <- cmdFrame
	return nil
}

// RegisterProgramHandler adds a FrameReceiver to the program ID registry for handling RX frames.
func (l *LinkMgr) RegisterProgramHandler(progID uint16, handler FrameReceiver) {
	l.registryMutex.Lock()
	l.RxRegistryProgram[progID] = handler
	l.registryMutex.Unlock()
}

// RegisterAddressHandler adds a FrameReceiver to the address registry for handling RX frames.
func (l *LinkMgr) RegisterAddressHandler(addr uint32, handler FrameReceiver) {
	l.registryMutex.Lock()
	l.RxRegistryAddress[addr] = handler
	l.registryMutex.Unlock()
}

// RegisterAllHandler adds a universal frame handler to the "Firehose"
func (l *LinkMgr) RegisterAllHandler(handler FrameReceiver) {
	l.registryMutex.Lock()
	defer l.registryMutex.Unlock()
	for _, hndl := range l.RxFirehose {
		if hndl == handler {
			return // No need to add since we already have it in the firehose?
		}
	}
	l.RxFirehose = append(l.RxFirehose, handler)
}

// DeregisterHandler searches all the registries to delete a handler
func (l *LinkMgr) DeregisterHandler(handler FrameReceiver) bool {
	var didPurge bool
	didPurge = false

	l.registryMutex.Lock()
	for k, v := range l.RxRegistryProgram {
		if handler == v {
			l.RxRegistryProgram[k] = nil
			didPurge = true
		}
	}
	for k, v := range l.RxRegistryAddress {
		if handler == v {
			l.RxRegistryAddress[k] = nil
			didPurge = true
		}
	}
	var newFirehose []FrameReceiver
	for _, hndl := range l.RxFirehose {
		if hndl != handler {
			newFirehose = append(newFirehose, hndl)
		} else {
			didPurge = true
		}
	}
	l.RxFirehose = newFirehose
	l.registryMutex.Unlock()
	return didPurge
}

// DeregisterProgramHandler removes the handler for the specified program ID, if present
func (l *LinkMgr) DeregisterProgramHandler(progID uint16) bool {
	var didPurge bool
	didPurge = false

	l.registryMutex.Lock()
	if l.RxRegistryProgram[progID] != nil {
		l.RxRegistryProgram[progID] = nil
		didPurge = true
	}
	l.registryMutex.Unlock()
	return didPurge
}

// DeregisterAddressHandler removes the handler for the specified address, if present
func (l *LinkMgr) DeregisterAddressHandler(addr uint32) bool {
	var didPurge bool
	didPurge = false

	l.registryMutex.Lock()
	if l.RxRegistryAddress[addr] != nil {
		l.RxRegistryAddress[addr] = nil
		didPurge = true
	}
	l.registryMutex.Unlock()
	return didPurge
}

// ExecRxHandler spawns a goroutine that monitors inbound RX frames
func (l *LinkMgr) ExecRxHandler() error {
	// Do a quick select to see if l.NpiDied was closed
	select {
	case <-l.NpiDied:
		return errors.New("NPI PHY link faulted")
	default:
	}

	go func(l *LinkMgr) {
		for {
			select {
			case <-l.NpiDied:
				return
			case otaFrame := <-l.FrameRX:
				var handler FrameReceiver
				l.registryMutex.Lock()
				handler = l.RxRegistryProgram[otaFrame.Program]
				l.registryMutex.Unlock()
				if handler != nil {
					ret := handler.Receive(l, otaFrame.Address, otaFrame.Program, otaFrame.Data)
					if !ret {
						continue // Do not attempt processing the frame any more
					}
				}
				l.registryMutex.Lock()
				handler = l.RxRegistryAddress[otaFrame.Address]
				l.registryMutex.Unlock()
				if handler != nil {
					ret := handler.Receive(l, otaFrame.Address, otaFrame.Program, otaFrame.Data)
					if !ret {
						continue // Do not attempt processing the frame any more
					}
				}
				l.registryMutex.Lock()
				firehoseList := l.RxFirehose
				l.registryMutex.Unlock()
				for _, handler = range firehoseList {
					ret := handler.Receive(l, otaFrame.Address, otaFrame.Program, otaFrame.Data)
					if !ret {
						break // Do not attempt processing the frame any more
					}
				}
			}
		}
	}(l)
	return nil
}

/* High-level Control API functions */

// GetIdentifier - Request compiled-in identifier string from NPI microcontroller's firmware
func (l *LinkMgr) GetIdentifier() (string, error) {
	stat, rpl, err := l.Ctrl(CONTROL_GET_IDENTIFIER, nil)
	if err != nil {
		return "", err
	}
	if stat != CONTROL_STATUS_OK {
		return "", errors.New("GetIdentifier error: " + Status(stat))
	}
	return string(rpl), nil
}

// GetRadio - Request current radio parameters
func (l *LinkMgr) GetRadio() (bool, uint32, int8, uint16, error) {
	stat, rpl, err := l.Ctrl(CONTROL_GET_RF, nil)
	if err != nil {
		return false, 0, 0, 0, err
	}
	if stat != CONTROL_STATUS_OK {
		return false, 0, 0, 0, errors.New("GetRadio error: " + Status(stat))
	}
	if len(rpl) != 8 {
		errStr := fmt.Sprintf("GetRadio: Reply payload was invalid size of %d (expected 8)", len(rpl))
		return false, 0, 0, 0, errors.New(errStr)
	}

	var rxOn bool
	var cFreq uint32
	var txPower int8
	var txTick uint16
	if rpl[0] != 0 {
		rxOn = true
	} else {
		rxOn = false
	}
	cFreq = uint32(rpl[1]) | (uint32(rpl[2]) << 8) | (uint32(rpl[3]) << 16) | (uint32(rpl[4]) << 24)
	txPower = int8(rpl[5])
	txTick = uint16(rpl[6]) | (uint16(rpl[7]) << 8)

	return rxOn, cFreq, txPower, txTick, nil
}

// GetAddresses - get IEEE address and alternate address
func (l *LinkMgr) GetAddresses() (uint32, uint32, error) {
	stat, rpl, err := l.Ctrl(CONTROL_GET_ADDRESSES, nil)
	if err != nil {
		return 0, 0, err
	}
	if stat != CONTROL_STATUS_OK {
		return 0, 0, errors.New("GetAddresses error: " + Status(stat))
	}
	if len(rpl) != 8 {
		errStr := fmt.Sprintf("GetAddresses: Reply payload was invalid size of %d (expected 8)", len(rpl))
		return 0, 0, errors.New(errStr)
	}

	var ieeeAddr, altAddr uint32
	ieeeAddr = uint32(rpl[0]) | (uint32(rpl[1]) << 8) | (uint32(rpl[2]) << 16) | (uint32(rpl[3]) << 24)
	altAddr = uint32(rpl[4]) | (uint32(rpl[5]) << 8) | (uint32(rpl[6]) << 16) | (uint32(rpl[7]) << 24)
	return ieeeAddr, altAddr, nil
}

// SetAlternateAddress - configure the secondary address the NPI RF microcontroller will listen to for incoming packets
// (particularly important for base stations)
func (l *LinkMgr) SetAlternateAddress(addr uint32) error {
	buf := make([]byte, 4)
	buf[0] = uint8(addr)
	buf[1] = uint8(addr >> 8)
	buf[2] = uint8(addr >> 16)
	buf[3] = uint8(addr >> 24)

	stat, _, err := l.Ctrl(CONTROL_SET_ALTERNATE_ADDR, buf)
	if err != nil {
		return err
	}
	if stat != CONTROL_STATUS_OK {
		return errors.New("SetAlternateAddress error: " + Status(stat))
	}
	return nil
}

// SetFrequency - configure the RF center frequency, good for Frequency Hopping or live reconfig
func (l *LinkMgr) SetFrequency(freq uint32) error {
	buf := make([]byte, 4)
	buf[0] = uint8(freq)
	buf[1] = uint8(freq >> 8)
	buf[2] = uint8(freq >> 16)
	buf[3] = uint8(freq >> 24)

	stat, _, err := l.Ctrl(CONTROL_SET_CENTERFREQ, buf)
	if err != nil {
		return err
	}
	if stat != CONTROL_STATUS_OK {
		return errors.New("SetFrequency error: " + Status(stat))
	}
	return nil
}

// SetPower - configure TX power in dBm (valid -10, 0-12, 14 but only under certain firmware builds)
func (l *LinkMgr) SetPower(dbm int8) error {
	buf := []byte{byte(dbm)}
	stat, _, err := l.Ctrl(CONTROL_SET_TXPOWER, buf)
	if err != nil {
		return err
	}
	if stat != CONTROL_STATUS_OK {
		return errors.New("SetPower error: " + Status(stat))
	}
	return nil
}

// SetTxInterval - configure the automatic tick for transmitting queued outbound frames
func (l *LinkMgr) SetTxInterval(ms uint16) error {
	buf := make([]byte, 2)
	buf[0] = uint8(ms)
	buf[1] = uint8(ms >> 8)
	stat, _, err := l.Ctrl(CONTROL_SET_TX_TICK, buf)
	if err != nil {
		return err
	}
	if stat != CONTROL_STATUS_OK {
		return errors.New("SetTxInterval error: " + Status(stat))
	}
	return nil
}

// RunTx - Trigger a transmit of any queued outbound RF frames
func (l *LinkMgr) RunTx() error {
	stat, _, err := l.Ctrl(CONTROL_RUN_TX, nil)
	if err != nil {
		return err
	}
	if stat != CONTROL_STATUS_OK {
		return errors.New("RunTx error: " + Status(stat))
	}
	return nil
}

// On - configure RX on or off
func (l *LinkMgr) On(onoff bool) error {
	var val uint8
	if onoff {
		val = 1
	} else {
		val = 0
	}
	buf := []byte{byte(val)}
	stat, _, err := l.Ctrl(CONTROL_SET_RF_ON, buf)
	if err != nil {
		return err
	}
	if stat != CONTROL_STATUS_OK {
		return errors.New("On error: " + Status(stat))
	}
	return nil
}

// SetLEDs - Switch the NPI MCU's master enable on/off
func (l *LinkMgr) SetLEDs(onoff bool) error {
	var val uint8
	if onoff {
		val = 1
	} else {
		val = 0
	}
	buf := []byte{byte(val)}
	stat, _, err := l.Ctrl(CONTROL_SET_LEDS, buf)
	if err != nil {
		return err
	}
	if stat != CONTROL_STATUS_OK {
		return errors.New("SetLEDs error: " + Status(stat))
	}
	return nil
}
