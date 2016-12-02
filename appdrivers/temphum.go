package appdrivers

import (
	"fmt"
	"github.com/spirilis/smacbase"
	"log"
	"math"
)

/* Temphum is based around a TI HDC1080 temperature + humidity sensor, albeit values doctored a bit.
 * Temperature is conveyed in a Signed 16-bit integer in Q12.3, so dividing by 8 gives the whole degrees C.
 * Multiply by 9/5 and add (32*8), then divide by 8 to get whole degrees F (with good precision)
 *
 * Humidity is a fraction in Q8 format, i.e. 0 = 0% humidity, 255 = 100% humidity.
 *
 * TODO: Persist data with timestamps into a database of some type.
 */

// TemperatureHumidity holds and handles 0x2002 packets
type TemperatureHumidity struct {
	DeviceIdHandler QueryDevice
	Logger          LogText
	LastSeenTemp    map[uint16]int16
	LastSeenHum     map[uint16]uint8
}

// NewTemperatureHumidity is the canonical way to create a TemperatureHumidity instance and bind it to a Link.
func NewTemperatureHumidity(l *smacbase.LinkMgr, g LogText, devIDHandler QueryDevice) *TemperatureHumidity {
	h := new(TemperatureHumidity)
	h.DeviceIdHandler = devIDHandler
	h.Logger = g
	h.LastSeenTemp = make(map[uint16]int16)
	h.LastSeenHum = make(map[uint16]uint8)

	l.RegisterProgramHandler(0x2002, h)
	return h
}

// Receive implements smacbase.FrameReceiver
func (t *TemperatureHumidity) Receive(l *smacbase.LinkMgr, srcAddr uint32, progID uint16, payload []byte) bool {
	if progID != 0x2002 {
		log.Printf("TemperatureHumidity.Receive: received frame for wrong progID=%04X, expected 0x2002", progID)
		return true // not sure why this packet was received here but keep processing
	}
	if len(payload) != 6 {
		log.Printf("TemperatureHumidity.Receive: received frame with invalid payload length, expected 6 bytes")
		return false // quit processing a bad packet
	}

	var temp int16
	var hum uint8
	var devid, tmp uint16
	var heaterOn string
	var fTemp, fHum, fDewpt float64 // For dewpoint calculation
	devid = uint16(payload[0]) | (uint16(payload[1]) << 8)
	tmp = uint16(payload[2]) | (uint16(payload[3]) << 8)
	temp = int16(tmp)
	hum = uint8(payload[4])
	if payload[5]&0x01 != 0 {
		heaterOn = " [HEATER]"
	}

	// Calculate dewpoint
	fTemp = float64(temp) / 8.0
	fHum = float64(hum) / 255.0
	// TD: =243.04*(LN(RH/100)+((17.625*T)/(243.04+T)))/(17.625-LN(RH/100)-((17.625*T)/(243.04+T)))
	// ^ From http://andrew.rsmas.miami.edu/bmcnoldy/Humidity.html
	fDewpt = 243.04 * (math.Log(fHum) + ((17.625 * fTemp) / (243.04 + fTemp))) / (17.625 - math.Log(fHum) - ((17.625 * fTemp) / (243.04 + fTemp)))

	t.LastSeenTemp[devid] = temp
	t.LastSeenHum[devid] = hum
	devDesc, _ := t.DeviceIdHandler.GetByDevice(devid)
	t.Logger.Printf("TempHum RX: [%s] - %.1f degF, %.1f%% RH, Dewpt %.1f degF%s\n", devDesc,
		(fTemp*9.0/5.0)+32.0,
		fHum*100.0,
		(fDewpt*9.0/5.0)+32.0,
		heaterOn)
	return false
}

// GetByDevice implements QueryDevice, returns a []int16 where position #0 is temperature in Celsius * 8, #1 is relative humidity in integer percentage (0-100)
func (t *TemperatureHumidity) GetByDevice(devID uint16) (interface{}, error) {
	var collection []int16

	if t.LastSeenTemp[devID] == 0 && t.LastSeenHum[devID] == 0 {
		return nil, NotFound(fmt.Sprintf("No information available for DeviceID=%04X", devID))
	}

	collection[0] = t.LastSeenTemp[devID]
	collection[1] = int16(t.LastSeenHum[devID]*100) / 255

	return collection, nil
}
