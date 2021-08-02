package main

import (
	"io"
	"net"
	"net/http"

	"github.com/CoiaPrant/zlog"

	proxyprotocol "github.com/pires/go-proxyproto"
	"golang.org/x/net/websocket"
)

type Addr struct {
	NetworkType   string
	NetworkString string
}

func (this *Addr) Network() string {
	return this.NetworkType
}

func (this *Addr) String() string {
	return this.NetworkString
}

func LoadWSRules(i string, r Rule) {
	if Setting.Listener.Has(i) {
		return
	}

	tcpaddress, err := net.ResolveTCPAddr("tcp", ":"+r.Listen)
	if err != nil {
		zlog.Error("Load failed [", r.UserID, "][", i, "] (WebSocket) Error: ", err)
		SendListenError(i)
		return
	}

	ln, err := net.ListenTCP("tcp", tcpaddress)
	if err == nil {
		Setting.Listener.Set(i, ln)
		zlog.Info("Loaded [", r.UserID, "][", i, "] (WebSocket)", r.Listen, " => ", ParseForward(r))
	} else {
		zlog.Error("Load failed [", r.UserID, "][", i, "] (WebSocket) Error: ", err)
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
		WS_Handle(i, ws)
	}))

	http.Serve(ln, Router)
}

func DeleteWSRules(i string, r Rule) {
	if ln, ok := Setting.Listener.Get(i); ok {
		Setting.Listener.Remove(i)
		ln.(*net.TCPListener).Close()
	}

	zlog.Info("Deleted [", r.UserID, "][", i, "] (WebSocket)", r.Listen, " => ", ParseForward(r))
}

func WS_Handle(i string, ws *websocket.Conn) {
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
		}, ws.LocalAddr()).Format()
		if err == nil {
			limitWrite(proxy, r, header)
		}
	}

	go copyIO(ws, proxy, r)
	copyIO(proxy, ws, r)
}
