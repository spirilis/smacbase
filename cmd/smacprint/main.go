package main

import (
	"fmt"
	"github.com/spirilis/smacbase"
	"github.com/spirilis/smacbase/appdrivers"
	"gopkg.in/alecthomas/kingpin.v2"
	"os"
)

var (
	serialPath = kingpin.Flag("device", "Path to serial port device").Required().String()
	baudRate   = kingpin.Flag("baud", "Serial port baudrate").Default("115200").Uint()
	centerFreq = kingpin.Flag("freq", "RF center frequency").Default("902800000").Uint32()
)

func main() {
	kingpin.Version("0.1")
	kingpin.Parse()

	link, err := smacbase.NewLinkMgr(*serialPath, *baudRate)
	if err != nil {
		fmt.Printf("Error opening NPI link: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Registering frame receiver drivers...")
	stdoutLogger := appdrivers.GenericStdout{}
	deviceIdHandler := appdrivers.NewDeviceIdRegistration(link)
	appdrivers.NewTemperatureHumidity(link, stdoutLogger, deviceIdHandler)
	printHandler := &appdrivers.FrameStdout{Logger: stdoutLogger}
	link.RegisterAllHandler(printHandler)
	pingHandler := appdrivers.PingHandler{Logger: stdoutLogger}
	link.RegisterProgramHandler(0x2003, pingHandler)
	fmt.Println("done")

	fmt.Printf("Configuring base station...")
	// Send a dummy control frame to clear out any badness in the UART buffers
	link.CtrlForget(smacbase.CONTROL_UNSQUELCH_HOST, nil)

	// Set base station addr, enable RX
	err = link.SetAlternateAddress(0xBACE0001)
	if _, ok := err.(smacbase.CtrlTimeout); ok {
		// Try once more
		err = link.SetAlternateAddress(0xBACE0001)
	}
	if err != nil {
		fmt.Printf("Error setting alternate addr: %v\n", err)
		os.Exit(1)
	}
	err = link.On(true)
	if _, ok := err.(smacbase.CtrlTimeout); ok {
		// Try once more
		err = link.On(true)
	}
	if err != nil {
		fmt.Printf("Error switching RX on: %v\n", err)
		os.Exit(1)
	}

	// Set center frequency to 902.8MHz
	err = link.SetFrequency(*centerFreq)
	if _, ok := err.(smacbase.CtrlTimeout); ok {
		// Try once more
		err = link.SetFrequency(*centerFreq)
	}
	if err != nil {
		fmt.Printf("Error changing center frequency: %v\n", err)
		os.Exit(1)
	}
	// Set TX power to 12dBm
	err = link.SetPower(12)
	if _, ok := err.(smacbase.CtrlTimeout); ok {
		// Try once more
		err = link.SetPower(12)
	}
	if err != nil {
		fmt.Printf("Error changing TX power: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("done")
	// main() doesn't do anything useful but we need to stay running for the rest of the goroutines to stay alive
	dummyChan := make(chan struct{})
	select {
	case <-dummyChan:
		os.Exit(1) // should never get here
	}
}
