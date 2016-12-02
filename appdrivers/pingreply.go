package appdrivers

import (
	"github.com/spirilis/smacbase"
	"log"
)

/* Ping reply implements a listener for Ping echo-requests (0x2003) and responds with an outbound packet
 * using echo-reply (0x2004) with an immediate control frame to issue TX.
 */

// PingHandler type doesn't do much; it just responds to ping requests
type PingHandler struct {
	Logger LogText
}

// Receive implements FrameReceiver
func (p PingHandler) Receive(l *smacbase.LinkMgr, srcAddr uint32, progID uint16, payload []byte) bool {
	if progID != 0x2003 {
		log.Printf("PingHandler.Receive: Handling invalid packet with progID=%04X", progID)
		return true
	}
	if len(payload) != 4 {
		log.Printf("PingHandler.Receive: Received ping echo-request with payload size = %d (expected 4)", len(payload))
	}

	var pingVal uint32
	pingVal = uint32(payload[0]) | (uint32(payload[1]) << 8) | (uint32(payload[2]) << 16) | (uint32(payload[3]) << 24)
	p.Logger.Printf("PingHandler.Receive: Responding to echo-request from src=%08X, payload = %04X\n", srcAddr, pingVal)
	l.Send(srcAddr, 0x2004, payload)
	err := l.RunTx()
	if err != nil {
		p.Logger.Printf("PingHandler.Receive: RunTx error: %v\n", err)
	}
	return false
}
