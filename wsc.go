package main

import (
	"github.com/CoiaPrant/zlog"
	"net"

	"golang.org/x/net/websocket"
)

func LoadWSCRules(i string, r Rule) {
	if _, ok := Setting.Listener.Load(i); ok {
		return
	}

	tcpaddress, err := net.ResolveTCPAddr("tcp", ":"+r.Listen)
	if err != nil {
		zlog.Error("Load failed [", r.UserID, "][", i, "] (WebSocket Client) Error: ", err)
		SendListenError(i)
		return
	}

	ln, err := net.ListenTCP("tcp", tcpaddress)

	if err == nil {
		Setting.Listener.Store(i, ln)
		zlog.Info("Loaded [", r.UserID, "][", i, "] (WebSocket Client) ", r.Listen, " => ", ParseForward(r))
	} else {
		zlog.Error("Load failed [", r.UserID, "][", i, "] (WebSocket Client) Error:", err)
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

		go wsc_handleRequest(conn, i)
	}
}

func DeleteWSCRules(i string, r Rule) {
	if ln, ok := Setting.Listener.LoadAndDelete(i); ok {
		ln.(*net.TCPListener).Close()
	}

	zlog.Info("Deleted [", r.UserID, "][", i, "] (WebSocket Client)", r.Listen, " => ", ParseForward(r))
}

func wsc_handleRequest(conn net.Conn, index string) {
	Setting.Rules.RLock()
	r := Setting.Config.Rules[index]
	Setting.Rules.RUnlock()

	if r.Status != "Active" && r.Status != "Created" {
		conn.Close()
		return
	}

	ws_config, err := websocket.NewConfig("ws://"+ParseForward(r)+"/ws/", "http://"+ParseForward(r)+"/ws/")
	if err != nil {
		conn.Close()
		return
	}
	ws_config.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/86.0.4240.198 Safari/537.36")
	ws_config.Header.Set("X-Forward-For", ParseAddrToIP(conn.RemoteAddr().String()))
	ws_config.Header.Set("X-Forward-Protocol", conn.RemoteAddr().Network())
	ws_config.Header.Set("X-Forward-Address", conn.RemoteAddr().String())
	proxy, err := websocket.DialConfig(ws_config)
	if err != nil {
		conn.Close()
		return
	}
	proxy.PayloadType = websocket.BinaryFrame

	go copyIO(conn, proxy, r)
	go copyIO(proxy, conn, r)
}
