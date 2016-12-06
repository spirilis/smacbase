package main

import (
	"fmt"
	"github.com/spirilis/smacbase"
	"gopkg.in/alecthomas/kingpin.v2"
	"os"
)

var (
	serialPath = kingpin.Flag("device", "Path to serial port device").Required().String()
	baudRate   = kingpin.Flag("baud", "Serial port baudrate").Default("115200").Uint()
)

func main() {
	kingpin.Version("0.1")
	kingpin.Parse()

	link, err := smacbase.NewLinkMgr(*serialPath, *baudRate)
	if err != nil {
		fmt.Printf("Error opening NPI link: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Deconfiguring base station...")
	// Send a dummy control frame to clear out any badness in the UART buffers
	link.CtrlForget(smacbase.CONTROL_UNSQUELCH_HOST, nil)

	// Disable RX
	err = link.On(false)
	if _, ok := err.(smacbase.CtrlTimeout); ok {
		// Try once more
		err = link.On(false)
	}
	if err != nil {
		fmt.Printf("Error switching RX off: %v\n", err)
		os.Exit(1)
	}
}
