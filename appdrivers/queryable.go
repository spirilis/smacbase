package appdrivers

/* queryable.go defines the QueryAddress and QueryDevice interface, which accept a 32-bit address
 * or a 16-bit Device ID and returns an interface{} which is expected to be correct for the context
 * of this data type.
 */

// QueryAddress implements GetByAddress(uint32) interface{}
type QueryAddress interface {
	GetByAddress(uint32) (interface{}, error)
}

// QueryDevice implements GetByDevice(uint16) interface{}
type QueryDevice interface {
	GetByDevice(uint16) (interface{}, error)
}

// NotFound is the most common Error type for a query
type NotFound string

func (n NotFound) Error() string {
	return string(n)
}
