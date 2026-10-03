package hid

// A Windows pen is display-associated; the auxiliary relative collection keeps
// wheel and middle/back/forward buttons without sending global absolute XY.
func windowsPointerReports(data []byte) [][]byte {
	if len(data) != 7 {
		return [][]byte{data}
	}
	flags := byte(4) // in range, including hover
	pressure := byte(0)
	if data[0]&3 != 0 {
		flags |= 1
		pressure = 4
	}
	if data[0]&2 != 0 {
		flags |= 2
	}
	return [][]byte{
		{1, flags, data[1], data[2], data[3], data[4], 0, pressure},
		{2, data[0] & 0x1c, 0, 0, data[5], data[6]},
	}
}
