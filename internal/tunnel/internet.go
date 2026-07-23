package tunnel

import (
	"net"
	"time"
)

func CheckInternet() bool {
	conn, err := net.DialTimeout("tcp", "1.1.1.1:443", 3*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
