package smacbase

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"testing"
	"time"
)

var SerialPort = "COM9"
var SerialBaudRate = 115200

func TestNpiControlSerialize(t *testing.T) {
	n := new(NpiControl)
	n.Command = 0x83
	n.Data = []byte("WHATISTHIS")
	ExpectedSerializedLength := 14

	srl := n.Serialize()
	var hexstream string
	for i := 0; i < len(srl); i++ {
		hexstream += fmt.Sprintf("%02x ", srl[i])
	}
	fmt.Printf("Serialized NpiControl: %s\n", hexstream)
	if len(srl) != ExpectedSerializedLength {
		t.Errorf("NpiControl Serialize() not producing proper byte length (expected %d, got %d)",
			ExpectedSerializedLength,
			len(srl))
		return
	}
}

func TestNpiRadioFrameSerialize(t *testing.T) {
	n := new(NpiRadioFrame)
	n.Address = 0xDEADBEEF
	n.Program = 0x6933
	n.Data = []byte("SIXTY NINE")
	ExpectedSerializedLength := 19

	srl := n.Serialize()
	var hexstream string
	for i := 0; i < len(srl); i++ {
		hexstream += fmt.Sprintf("%02x ", srl[i])
	}
	fmt.Printf("Serialized NpiRadioFrame: %s\n", hexstream)
	if len(srl) != ExpectedSerializedLength {
		t.Errorf("NpiRadioFrame Serialize() not producing proper byte length (expected %d, got %d)",
			ExpectedSerializedLength,
			len(srl))
		return
	}
}

/* For dry-testing the PHY, we need an io.ReadWriteCloser that can be used with test harnesses. */
type TestLink struct {
	CannedData  []byte
	Dump        bytes.Buffer
	WaitForMore chan bool
	IsActive    bool
}

func (l *TestLink) Read(p []byte) (int, error) {
	if !l.IsActive {
		return 0, errors.New("Not open anymore")
	}
	if len(l.CannedData) == 0 {
		select {
		case <-l.WaitForMore:
			break
		}
	}
	maxLen := len(p)
	if maxLen > 10 {
		maxLen = 10
	}
	if maxLen < len(l.CannedData) {
		copy(p, l.CannedData[:maxLen])
		l.CannedData = l.CannedData[maxLen:]
		return maxLen, nil
	}
	maxLen = len(l.CannedData)
	copy(p, l.CannedData)
	l.CannedData = l.CannedData[:0]
	return maxLen, nil
}

func (l *TestLink) Write(p []byte) (int, error) {
	if !l.IsActive {
		return 0, errors.New("Not open anymore")
	}

	var hexstream string
	for i := 0; i < len(p); i++ {
		hexstream += fmt.Sprintf("%02x ", p[i])
	}
	log.Printf("TestLink.Write() appending %d bytes: %s", len(p), hexstream)
	return l.Dump.Write(p)
}

func (l *TestLink) Close() error {
	l.IsActive = false
	return nil
}

var defaultReadData = []byte{'C', 'O', 'A', 'L', 'C', 'A', 'R', 'S',
	0xAE, 0xEF, 0xBE, 0xAD, 0xDE, 0x33, 0x69, 0x0A,
	'S', 'I', 'X', 'T', 'Y', ' ', 'N', 'I', 'N', 'E', 0x11,
	'D', 'E', 'R', 'A', 'I', 'L', 'E', 'D'}

func TestRunNPI(t *testing.T) {
	// func RunNPI(phy io.ReadWriteCloser, frameXmit chan *NpiRadioFrame, frameRecv chan *NpiRadioFrame, ctrlXmit chan *NpiControl) error {
	TestPhy := new(TestLink)
	TestPhy.IsActive = true
	TestPhy.CannedData = defaultReadData
	TestPhy.WaitForMore = make(chan bool)

	frameXmit := make(chan *NpiRadioFrame, 4)
	frameRecv := make(chan *NpiRadioFrame, 4)
	ctrlXmit := make(chan *NpiControl, 4)
	npiFault := make(chan struct{})
	go RunNPI(TestPhy, frameXmit, frameRecv, ctrlXmit, npiFault)

	exampleCtrlFrame := NewControl(0xDE, []byte{0x01, 0x02, 0x03, 0x04, 0xFF})

	var frameCount int
	tckr := time.Tick(time.Second * 5)
	ectrlTck := time.After(time.Second * 1)
	for {
		select {
		case <-npiFault:
			t.Errorf("RunNPI Fault detected")
			return
		case n := <-frameRecv:
			fmt.Printf("Received frame: %q\n", *n)
			if n != nil {
				frameCount++
			}
		case <-ectrlTck:
			ctrlXmit <- exampleCtrlFrame
		case <-tckr:
			if frameCount < 1 {
				t.Errorf("Did not receive any valid frames")
			}
			return
		}
	}
}

type TestRxHandler struct{}

func (h *TestRxHandler) Receive(l *LinkMgr, addr uint32, prog uint16, data []byte) bool {
	fmt.Printf("Received packet: addr=0x%08X, prog=0x%04X, data=[%s]\n", addr, prog, string(data))
	return true
}

func TestLinkMgr(t *testing.T) {
	TestPhy := new(TestLink)
	TestPhy.IsActive = true
	TestPhy.CannedData = defaultReadData
	TestPhy.WaitForMore = make(chan bool)

	testHandler := new(TestRxHandler)

	l := new(LinkMgr)
	l.FrameTX = make(chan *NpiRadioFrame)
	l.FrameRX = make(chan *NpiRadioFrame)
	l.CtrlTX = make(chan *NpiControl)
	l.NpiDied = make(chan struct{})
	l.Phy = TestPhy

	l.RxRegistryProgram = make(map[uint16]FrameReceiver)
	l.RxRegistryAddress = make(map[uint32]FrameReceiver)

	l.RxRegistryProgram[0x6933] = testHandler
	l.RxRegistryAddress[0xDEADBEEF] = testHandler
	l.RxFirehose = []FrameReceiver{testHandler}

	fmt.Println("Should see 3 prints of the test packet:")

	go RunNPI(l.Phy, l.FrameTX, l.FrameRX, l.CtrlTX, l.NpiDied)
	// Launch a goroutine which dispatches received RX frames
	err := l.ExecRxHandler()
	if err != nil {
		t.Errorf("TestLinkMgr error executing RX handler: %v\n", err)
		return
	}

	tck := time.After(time.Second * 3)
	select {
	case <-l.NpiDied:
		t.Errorf("Unexpected closing of PHY")
		return
	case <-tck:
		break // Continue with the next test
	}

	l.DeregisterHandler(testHandler)
	fmt.Println("Should see 0 prints of the test packet:")

	TestPhy.CannedData = defaultReadData
	TestPhy.WaitForMore <- true // Allow NPI goroutines to continue Read()'ing TestPhy
	tck = time.After(time.Second * 3)
	select {
	case <-l.NpiDied:
		t.Errorf("Unexpected closing of PHY")
		return
	case <-tck:
		return
	}
}

func TestUint32ToBuf(t *testing.T) {
	var testLongWord uint32
	buf := make([]byte, 4)

	testLongWord = 0xDEADBEEF
	buf[0] = uint8(testLongWord)
	buf[1] = uint8(testLongWord >> 8)
	buf[2] = uint8(testLongWord >> 16)
	buf[3] = uint8(testLongWord >> 24)

	if buf[0] != 0xEF || buf[1] != 0xBE || buf[2] != 0xAD || buf[3] != 0xDE {
		t.Errorf("[]byte composition was invalid: %08X = %02X %02X %02X %02X", testLongWord, buf[0], buf[1], buf[2], buf[3])
	}
}

func TestCC1310GetIdentifier(t *testing.T) {
	l, err := NewLinkMgr(SerialPort, uint(SerialBaudRate))
	if err != nil {
		t.Errorf("Error starting NPI Link: %v", err)
		return
	}

	id, err := l.GetIdentifier()
	if err != nil {
		t.Errorf("Error getting NPI Identifier: %v", err)
		return
	}
	fmt.Printf("NPI identifier string: [%s]\n", id)
	l.Close()
}

func TestCC1310GetRadio(t *testing.T) {
	l, err := NewLinkMgr(SerialPort, uint(SerialBaudRate))
	if err != nil {
		t.Errorf("Error starting NPI Link: %v", err)
		return
	}

	rxOn, centerFreq, txPower, txTick, err := l.GetRadio()
	if err != nil {
		t.Errorf("Error getting Radio params: %v", err)
		return
	}
	// 	return rxOn, cFreq, txPower, txTick, nil
	fmt.Printf("RX: %v, Freq: %d, TXpower: %d dBm, TXtick: %d\n", rxOn, centerFreq, txPower, txTick)
	l.Close()
}

func TestCC1310SendDebugFrame(t *testing.T) {
	l, err := NewLinkMgr(SerialPort, uint(SerialBaudRate))
	if err != nil {
		t.Errorf("Error starting NPI Link: %v", err)
		return
	}

	dbgText := "S.BOOGERY BUNZ!!"
	l.Send(0xDEAD0001, 0xFFFF, []byte(dbgText))
	err = l.RunTx()
	if err != nil {
		t.Errorf("RunTx error: %v\n", err)
		return
	}
	l.Close()
}

func TestCC1310ToggleRxOn(t *testing.T) {
	l, err := NewLinkMgr(SerialPort, uint(SerialBaudRate))
	if err != nil {
		t.Errorf("Error starting NPI Link: %v", err)
		return
	}

	err = l.On(true)
	if err != nil {
		t.Errorf("Error issuing On(true): %v\n", err)
		return
	}
	tck := time.After(time.Second * 1)
	select {
	case <-tck:
	}
	err = l.On(false)
	if err != nil {
		t.Errorf("Error issuing On(false): %v\n", err)
		return
	}
	tck = time.After(time.Second * 1)
	select {
	case <-tck:
	}
	err = l.On(true)
	if err != nil {
		t.Errorf("Error issuing On(true): %v\n", err)
		return
	}
	tck = time.After(time.Second * 1)
	select {
	case <-tck:
	}
	err = l.On(false)
	if err != nil {
		t.Errorf("Error issuing On(false): %v\n", err)
		return
	}
	tck = time.After(time.Second * 1)
	select {
	case <-tck:
	}
	err = l.On(true)
	if err != nil {
		t.Errorf("Error issuing On(true): %v\n", err)
		return
	}
}
