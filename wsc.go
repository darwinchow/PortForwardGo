package main

import (
	"PortForwardGo/zlog"
	"net"

	"golang.org/x/net/websocket"
)

func LoadWSCRules(i string) {
	Setting.Listener.Turn.RLock()
	if _, ok := Setting.Listener.WSC[i]; ok {
		return
	}
	Setting.Listener.Turn.RUnlock()

	Setting.Rules.RLock()
	r := Setting.Config.Rules[i]
	Setting.Rules.RUnlock()

	tcpaddress, _ := net.ResolveTCPAddr("tcp", ":"+r.Listen)
	ln, err := net.ListenTCP("tcp", tcpaddress)
	if err == nil {
		zlog.Info("Loaded [", r.UserID, "][", i, "] (WebSocket Client) ", r.Listen, " => ", ParseForward(r))
	} else {
		zlog.Error("Load failed [", r.UserID, "][", i, "] (WebSocket Client) Error:", err)
		SendListenError(i)
		return
	}
	Setting.Listener.Turn.Lock()
	Setting.Listener.WSC[i] = ln
	Setting.Listener.Turn.Unlock()
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

func DeleteWSCRules(i string) {
	Setting.Listener.Turn.Lock()
	if _, ok := Setting.Listener.WSC[i]; ok {
		Setting.Listener.WSC[i].Close()
		delete(Setting.Listener.WSC, i)
	}
	Setting.Listener.Turn.Unlock()

	Setting.Rules.Lock()
	r := Setting.Config.Rules[i]
	delete(Setting.Config.Rules, i)
	Setting.Rules.Unlock()
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
		_ = conn.Close()
		return
	}
	ws_config.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/86.0.4240.198 Safari/537.36")
	ws_config.Header.Set("X-Forward-For", ParseAddrToIP(conn.RemoteAddr().String()))
	ws_config.Header.Set("X-Forward-Protocol", conn.RemoteAddr().Network())
	ws_config.Header.Set("X-Forward-Address", conn.RemoteAddr().String())
	proxy, err := websocket.DialConfig(ws_config)
	if err != nil {
		_ = conn.Close()
		return
	}
	proxy.PayloadType = websocket.BinaryFrame

	go copyIO(conn, proxy, r)
	go copyIO(proxy, conn, r)
}
