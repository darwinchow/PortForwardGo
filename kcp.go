package main

import (
	"PortForwardGo/zlog"
	"net"
	"time"

	proxyprotocol "github.com/pires/go-proxyproto"
	kcp "github.com/xtaci/kcp-go"
)

func LoadKCPRules(i string) {
	Setting.Rules.RLock()
	ln, err := kcp.ListenWithOptions(":"+Setting.Config.Rules[i].Listen, nil, 10, 3)
	if err == nil {
		zlog.Info("Loaded [", i, "] (KCP)", Setting.Config.Rules[i].Listen, " => ", Setting.Config.Rules[i].Forward)
	} else {
		zlog.Error("Load failed [", i, "] (KCP) Error: ", err)
		Setting.Rules.RUnlock()
		SendListenError(i)
		return
	}
	Setting.Listener.KCP[i] = ln
	Setting.Rules.RUnlock()
	for {
		conn, err := ln.Accept()

		if err != nil {
			if err, ok := err.(net.Error); ok && err.Temporary() {
				continue
			}
			break
		}

		Setting.Rules.RLock()
		r := Setting.Config.Rules[i]
		Setting.Rules.RUnlock()

		if r.Status != "Active" && r.Status != "Created" {
			conn.Close()
			continue
		}

		go kcp_handleRequest(conn, i, r)
	}
}

func DeleteKCPRules(i string) {
	if _, ok := Setting.Listener.KCP[i]; ok {
		err := Setting.Listener.KCP[i].Close()
		for err != nil {
			time.Sleep(time.Second)
			err = Setting.Listener.KCP[i].Close()
		}
		delete(Setting.Listener.KCP, i)
	}
	Setting.Rules.Lock()
	zlog.Info("Deleted [", i, "] (KCP)", Setting.Config.Rules[i].Listen, " => ", Setting.Config.Rules[i].Forward)
	delete(Setting.Config.Rules, i)
	Setting.Rules.Unlock()
}

func kcp_handleRequest(conn net.Conn, index string, r Rule) {
	proxy, err := kcp.Dial(r.Forward)
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
