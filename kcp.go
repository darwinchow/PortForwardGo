package main

import (
	"net"

	"github.com/CoiaPrant/zlog"
	kcp "github.com/xtaci/kcp-go"
)

func LoadKCPRules(i string, r Rule) {
	if _, ok := Setting.Listener.Load(i); ok {
		return
	}

	ln, err := kcp.ListenWithOptions(":"+r.Listen, nil, 10, 3)

	if err == nil {
		Setting.Listener.Store(i, ln)
		zlog.Info("Loaded [", r.UserID, "][", i, "] (KCP)", r.Listen, " => ", ParseForward(r))
	} else {
		zlog.Error("Load failed [", r.UserID, "][", i, "] (KCP) Error: ", err)
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

		go kcp_handleRequest(conn, i)
	}
}

func DeleteKCPRules(i string, r Rule) {
	if ln, ok := Setting.Listener.LoadAndDelete(i); ok {
		ln.(*kcp.Listener).Close()
	}

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

	go copyIO(conn, proxy, r)
	go copyIO(proxy, conn, r)
}
