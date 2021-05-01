package main

import (
	"github.com/CoiaPrant/zlog"
	"io"
	"net"
	"net/http"

	proxyprotocol "github.com/pires/go-proxyproto"
	"golang.org/x/net/websocket"
)

func LoadWSSRules(i string) {
	Setting.Listener.Turn.RLock()
	if _, ok := Setting.Listener.WSS[i]; ok {
		return
	}
	Setting.Listener.Turn.RUnlock()

	Setting.Rules.RLock()
	r := Setting.Config.Rules[i]
	Setting.Rules.RUnlock()

	tcpaddress, _ := net.ResolveTCPAddr("tcp", ":"+r.Listen)
	ln, err := net.ListenTCP("tcp", tcpaddress)
	if err == nil {
		Setting.Listener.Turn.Lock()
		Setting.Listener.WSS[i] = ln
		Setting.Listener.Turn.Unlock()
		zlog.Info("Loaded [", r.UserID, "][", i, "] (WebSocket TLS)", r.Listen, " => ", ParseForward(r))
	} else {
		zlog.Error("Load failed [", r.UserID, "][", i, "] (Websocket TLS) Error: ", err)
		SendListenError(i)
		return
	}

	Router := http.NewServeMux()
	Router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, Page404)
		return
	})

	Router.Handle("/ws/", websocket.Handler(func(ws *websocket.Conn) {
		WSS_Handle(i, ws)
	}))

	http.ServeTLS(ln, Router, certFile, keyFile)
}

func DeleteWSSRules(i string) {
	Setting.Listener.Turn.Lock()
	if _, ok := Setting.Listener.WSS[i]; ok {
		Setting.Listener.WSS[i].Close()
		delete(Setting.Listener.WSS, i)
	}
	Setting.Listener.Turn.Unlock()

	Setting.Rules.Lock()
	r := Setting.Config.Rules[i]
	delete(Setting.Config.Rules, i)
	Setting.Rules.Unlock()

	zlog.Info("Deleted [", r.UserID, "][", i, "] (WebSocket TLS)", r.Listen, " => ", ParseForward(r))
}

func WSS_Handle(i string, ws *websocket.Conn) {
	ws.PayloadType = websocket.BinaryFrame
	Setting.Rules.RLock()
	r := Setting.Config.Rules[i]
	Setting.Rules.RUnlock()

	if r.Status != "Active" && r.Status != "Created" {
		ws.Close()
		return
	}

	proxy, err := net.Dial("tcp", ParseForward(r))
	if err != nil {
		ws.Close()
		return
	}

	if r.ProxyProtocolVersion != 0 {
		header, err := proxyprotocol.HeaderProxyFromAddrs(byte(r.ProxyProtocolVersion), &Addr{
			NetworkType:   ws.Request().Header.Get("X-Forward-Protocol"),
			NetworkString: ws.Request().Header.Get("X-Forward-Address"),
		}, proxy.LocalAddr()).Format()
		if err == nil {
			limitWrite(proxy, r.UserID, header)
		}
	}

	go copyIO(ws, proxy, r)
	copyIO(proxy, ws, r)
}
