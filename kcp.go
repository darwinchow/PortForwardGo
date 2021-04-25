package main

import (
	"github.com/CoiaPrant/zlog"
	"net"

	proxyprotocol "github.com/pires/go-proxyproto"
	kcp "github.com/xtaci/kcp-go"
)

func LoadKCPRules(i string) {
	Setting.Listener.Turn.RLock()
	if _, ok := Setting.Listener.KCP[i]; ok {
		return
	}
	Setting.Listener.Turn.RUnlock()

	Setting.Rules.RLock()
	r := Setting.Config.Rules[i]
	Setting.Rules.RUnlock()

	ln, err := kcp.ListenWithOptions(":"+r.Listen, nil, 10, 3)
	if err == nil {
		zlog.Info("Loaded [", r.UserID, "][", i, "] (KCP)", r.Listen, " => ", ParseForward(r))
	} else {
		zlog.Error("Load failed [", r.UserID, "][", i, "] (KCP) Error: ", err)
		SendListenError(i)
		return
	}
	Setting.Listener.Turn.Lock()
	Setting.Listener.KCP[i] = ln
	Setting.Listener.Turn.Unlock()
	for {
		conn, err := ln.Accept()

		if err != nil {
			if err, ok := err.(net.Error); ok && err.Temporary() {
				continue
			}
			break
		}

		go kcp_handleRequest(conn, i)
	}
}

func DeleteKCPRules(i string) {
	Setting.Listener.Turn.Lock()
	if _, ok := Setting.Listener.KCP[i]; ok {
		Setting.Listener.KCP[i].Close()
		delete(Setting.Listener.KCP, i)
	}
	Setting.Listener.Turn.Unlock()

	Setting.Rules.Lock()
	r := Setting.Config.Rules[i]
	delete(Setting.Config.Rules, i)
	Setting.Rules.Unlock()
	zlog.Info("Deleted [", r.UserID, "][", i, "] (KCP)", r.Listen, " => ", ParseForward(r))
}

func kcp_handleRequest(conn net.Conn, index string) {

	Setting.Rules.RLock()
	r := Setting.Config.Rules[index]
	Setting.Rules.RUnlock()

	if r.Status != "Active" && r.Status != "Created" {
		conn.Close()
		return
	}

	proxy, err := kcp.Dial(ParseForward(r))
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
