package main

import (
	"errors"
	"github.com/CoiaPrant/zlog"
	"net"
	"time"
)

type UDPConn struct {
	Connected bool
	Conn      *(net.UDPConn)
	Cache     chan []byte
	RAddr     net.Addr
	LAddr     net.Addr
}

func NewUDPConn(conn *(net.UDPConn), addr net.Addr) *UDPConn {
	return &UDPConn{
		Connected: true,
		Conn:      conn,
		Cache:     make(chan []byte, 16),
		RAddr:     addr,
		LAddr:     conn.LocalAddr(),
	}
}

func (this *UDPConn) Close() error {
	this.Connected = false
	return nil
}

func (this *UDPConn) Read(b []byte) (n int, err error) {
	if !this.Connected {
		return 0, errors.New("udp conn has closed")
	}

	select {
	case <-time.After(16 * time.Second):
		return 0, errors.New("i/o read timeout")
	case data := <-this.Cache:
		n := len(data)
		copy(b, data)
		return n, nil
	}
}

func (this *UDPConn) Write(b []byte) (int, error) {
	if !this.Connected {
		return 0, errors.New("udp conn has closed")
	}
	return this.Conn.WriteTo(b, this.RAddr)
}

func (this *UDPConn) RemoteAddr() net.Addr {
	return this.RAddr
}

func (this *UDPConn) LocalAddr() net.Addr {
	return this.LAddr
}

func (this *UDPConn) SetDeadline(t time.Time) error {
	return this.Conn.SetDeadline(t)
}

func (this *UDPConn) SetReadDeadline(t time.Time) error {
	return this.Conn.SetReadDeadline(t)
}

func (this *UDPConn) SetWriteDeadline(t time.Time) error {
	return this.Conn.SetWriteDeadline(t)
}

func LoadUDPRules(i string, r Rule) {
	if Setting.Listener.Has(i) {
		return
	}

	address, err := net.ResolveUDPAddr("udp", ":"+r.Listen)
	if err != nil {
		zlog.Error("Load failed [", r.UserID, "][", i, "] (UDP) Error: ", err)
		SendListenError(i)
		return
	}

	ln, err := net.ListenUDP("udp", address)

	if err == nil {
		Setting.Listener.Set(i, ln)
		zlog.Info("Loaded [", r.UserID, "][", i, "] (UDP)", r.Listen, " => ", ParseForward(r))
	} else {
		zlog.Error("Load failed [", r.UserID, "][", i, "] (UDP) Error: ", err)
		SendListenError(i)
		return
	}

	AcceptUDP(ln, i)
}

func DeleteUDPRules(i string, r Rule) {
	if ln, ok := Setting.Listener.Get(i); ok {
		Setting.Listener.Remove(i)
		ln.(*net.UDPConn).Close()
	}

	zlog.Info("Deleted [", r.UserID, "][", i, "] (UDP)", r.Listen, " => ", ParseForward(r))
}

func AcceptUDP(serv *net.UDPConn, index string) {

	table := make(map[string]*UDPConn)
	for {
		buf := make([]byte, 32*1024)

		n, addr, err := serv.ReadFrom(buf)
		if err != nil {
			if err, ok := err.(net.Error); ok && err.Temporary() {
				continue
			}
			break
		}

		go func() {
			buf = buf[:n]

			if d, ok := table[addr.String()]; ok {
				if d.Connected {
					d.Cache <- buf
					return
				} else {
					delete(table, addr.String())
				}
			}

			conn := NewUDPConn(serv, addr)
			table[addr.String()] = conn
			conn.Cache <- buf

			udp_handleRequest(conn, index)
		}()
	}
}

func udp_handleRequest(conn net.Conn, index string) {
	Setting.Rules.RLock()
	r := Setting.Config.Rules[index]
	Setting.Rules.RUnlock()

	if r.Status != "Active" && r.Status != "Created" {
		conn.Close()
		return
	}

	proxy, err := net.DialTimeout("udp", ParseForward(r), 10*time.Second)
	if err != nil {
		conn.Close()
		return
	}

	go copyIO(conn, proxy, r)
	go copyIO(proxy, conn, r)
}
