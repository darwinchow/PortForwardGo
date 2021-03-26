package main

import (
	"PortForwardGo/zlog"
	"net"

	proxyprotocol "github.com/pires/go-proxyproto"
)

func LoadTCPRules(i string) {
	Setting.Rules.RLock()
	r := Setting.Config.Rules[i]
	Setting.Rules.RUnlock()

	tcpaddress, _ := net.ResolveTCPAddr("tcp", ":"+r.Listen)
	ln, err := net.ListenTCP("tcp", tcpaddress)
	if err == nil {
		zlog.Info("Loaded [", i, "] (TCP)", r.Listen, " => ", ParseForward(r))
	} else {
		zlog.Error("Load failed [", i, "] (TCP) Error: ", err)
		SendListenError(i)
		return
	}
	Setting.Listener.TCP[i] = ln
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

func DeleteTCPRules(i string) {
	if _, ok := Setting.Listener.TCP[i]; ok {
		Setting.Listener.TCP[i].Close()
		delete(Setting.Listener.TCP, i)
	}
	Setting.Rules.Lock()
	r := Setting.Config.Rules[i]
	delete(Setting.Config.Rules, i)
	Setting.Rules.Unlock()
	zlog.Info("Deleted [", i, "] (TCP)", r.Listen, " => ",ParseForward(r))
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
		header, err := proxyprotocol.HeaderProxyFromAddrs(byte(r.ProxyProtocolVersion), conn.RemoteAddr(), conn.LocalAddr()).Format()
		if err == nil {
			limitWrite(proxy, r.UserID, header)
		}
	}

	go copyIO(conn, proxy, r)
	go copyIO(proxy, conn, r)
}
