package app

import (
	"crypto/tls"
	"errors"
	"golang.org/x/sys/unix"
	"net"
)

func transmitted(conn net.Conn) (uint64, error) {
	if tlsConn, ok := conn.(*tls.Conn); ok {
		conn = tlsConn.NetConn()
	}
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return 0, errors.New("not a TCP connection")
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		return 0, err
	}
	var bytes uint64
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		info, e := unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO)
		socketErr = e
		if e == nil {
			bytes = info.Bytes_acked
		}
	})
	return bytes, errors.Join(err, socketErr)
}
