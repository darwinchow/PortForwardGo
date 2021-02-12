package main

import (
	"PortForwardGo/zlog"
	"net"
	"time"

	proxyprotocol "github.com/pires/go-proxyproto"
)

func LoadTCPRules(i string) {
	Setting.Rules.RLock()
	tcpaddress, _ := net.ResolveTCPAddr("tcp", ":"+Setting.Config.Rules[i].Listen)
	ln, err := net.ListenTCP("tcp", tcpaddress)
	if err == nil {
		zlog.Info("Loaded [", i, "] (TCP)", Setting.Config.Rules[i].Listen, " => ", Setting.Config.Rules[i].Forward)
	} else {
		Setting.Rules.RUnlock()
		zlog.Error("Load failed [", i, "] (TCP) Error: ", err)
		SendListenError(i)
		return
	}
	Setting.Listener.TCP[i] = ln
	Setting.Rules.RUnlock()
	for {
		conn, err := ln.Accept()

		if err != nil {
			if err, ok := err.(net.Error); ok && err.Temporary() {
				continue
			}
			break
		}

		go func() {
			Setting.Rules.RLock()
			rule := Setting.Config.Rules[i]
			Setting.Rules.RUnlock()

			if rule.Status != "Active" && rule.Status != "Created" {
				conn.Close()
				return
			}

			go tcp_handleRequest(conn, i, rule)
		}()
	}
}

func DeleteTCPRules(i string) {
	if _, ok := Setting.Listener.TCP[i]; ok {
		err := Setting.Listener.TCP[i].Close()
		for err != nil {
			time.Sleep(time.Second)
			err = Setting.Listener.TCP[i].Close()
		}
		delete(Setting.Listener.TCP, i)
	}
	Setting.Rules.Lock()
	zlog.Info("Deleted [", i, "] (TCP)", Setting.Config.Rules[i].Listen, " => ", Setting.Config.Rules[i].Forward)
	delete(Setting.Config.Rules, i)
	Setting.Rules.Unlock()
}

func tcp_handleRequest(conn net.Conn, index string, r Rule) {
	proxy, err := net.Dial("tcp", r.Forward)
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

	go copyIO(conn, proxy, r.UserID)
	go copyIO(proxy, conn, r.UserID)
}
