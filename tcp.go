package main

import (
	"net"

	"github.com/CoiaPrant/zlog"

	proxyprotocol "github.com/pires/go-proxyproto"
)

func LoadTCPRules(i string, r Rule) {

	if Setting.Listener.Has(i) {
		return
	}

	tcpaddress, err := net.ResolveTCPAddr("tcp", ":"+r.Listen)
	if err != nil {
		zlog.Error("Load failed [", r.UserID, "][", i, "] (TCP) Error: ", err)
		SendListenError(i)
		return
	}

	ln, err := net.ListenTCP("tcp", tcpaddress)

	if err == nil {
		Setting.Listener.Set(i, ln)
		zlog.Info("Loaded [", r.UserID, "][", i, "] (TCP)", r.Listen, " => ", ParseForward(r))
	} else {
		zlog.Error("Load failed [", r.UserID, "][", i, "] (TCP) Error: ", err)
		SendListenError(i)
		return
	}

	for {
		conn, err := ln.Accept()

		if err != nil {
			if err, ok := err.(net.Error); ok && err.Temporary() {
				continue
			}
			break
		}

		go tcp_handleRequest(conn, i)
	}
}

func DeleteTCPRules(i string, r Rule) {
	if ln, ok := Setting.Listener.Get(i); ok {
		Setting.Listener.Remove(i)
		ln.(*net.TCPListener).Close()
	}

	zlog.Info("Deleted [", r.UserID, "][", i, "] (TCP)", r.Listen, " => ", ParseForward(r))
}

func tcp_handleRequest(conn net.Conn, index string) {
	Setting.Rules.RLock()
	r := Setting.Config.Rules[index]
	Setting.Rules.RUnlock()

	if r.Status != "Active" && r.Status != "Created" {
		conn.Close()
		return
	}

	proxy, err := net.Dial("tcp", ParseForward(r))
	if err != nil {
		conn.Close()
		return
	}

	if r.ProxyProtocolVersion != 0 {
		header, err := proxyprotocol.HeaderProxyFromAddrs(byte(r.ProxyProtocolVersion), conn.LocalAddr(), conn.RemoteAddr()).Format()
		if err == nil {
			limitWrite(proxy, r, header)
		}
	}
	go copyIO(conn, proxy, r)
	go copyIO(proxy, conn, r)
}
