package main

import (
	"PortForwardGo/zlog"
	"bufio"
	"container/list"
	"net"
	"strings"

	proxyprotocol "github.com/pires/go-proxyproto"
)

func HttpInit() {
	zlog.Info("[HTTP] Listening ", Setting.Config.Listen["Http"].Port)
	l, err := net.Listen("tcp", ":"+Setting.Config.Listen["Http"].Port)
	if err != nil {
		zlog.Error("[HTTP] Listen failed , Error: ", err)
		return
	}

	Setting.Listener.Turn.Lock()
	Setting.Listener.HTTPServer = l
	Setting.Listener.Turn.Unlock()
	for {
		c, err := l.Accept()
		if err != nil {
			if err, ok := err.(net.Error); ok && err.Temporary() {
				continue
			}
			break
		}
		go http_handle(c)
	}
}

func LoadHttpRules(i string) {
	Setting.Rules.RLock()
	r := Setting.Config.Rules[i]
	Setting.Rules.RUnlock()

	Setting.Listener.Turn.RLock()
	if _, ok := Setting.Listener.HTTP[strings.ToLower(r.Listen)]; ok {
		return
	}
	Setting.Listener.Turn.RUnlock()
	zlog.Info("Loaded [", r.UserID, "][", i, "] (HTTP)", r.Listen, " => ", ParseForward(r))
	Setting.Listener.Turn.Lock()
	Setting.Listener.HTTP[strings.ToLower(r.Listen)] = i
	Setting.Listener.Turn.Unlock()
}

func DeleteHttpRules(i string) {
	Setting.Rules.Lock()
	r := Setting.Config.Rules[i]
	delete(Setting.Config.Rules, i)
	Setting.Rules.Unlock()

	zlog.Info("Deleted [", r.UserID, "][", i, "] (HTTP)", r.Listen, " => ", ParseForward(r))

	Setting.Listener.Turn.Lock()
	delete(Setting.Listener.HTTP, strings.ToLower(r.Listen))
	Setting.Listener.Turn.Unlock()
}

func http_handle(conn net.Conn) {
	headers := bufio.NewReader(conn)
	hostname := ""
	readLines := list.New()
	for {
		bytes, _, error := headers.ReadLine()
		if error != nil {
			conn.Close()
			return
		}
		line := string(bytes)
		readLines.PushBack(line)

		if line == "" {
			break
		}

		if strings.HasPrefix(line, "X-Forward-For: ") == false {
			readLines.PushBack("X-Forward-For: " + ParseAddrToIP(conn.RemoteAddr().String()))
		}

		if strings.HasPrefix(line, "Host: ") {
			hostname = ParseHostToName(strings.TrimPrefix(line, "Host: "))
		}
	}

	if hostname == "" {
		zlog.Error("[HTTP] No Hostname")
		conn.Write([]byte(HttpStatus(503)))
		conn.Write([]byte("\n"))
		conn.Write([]byte(Page503))
		conn.Close()
		return
	}

	i, ok := Setting.Listener.HTTP[hostname]
	if !ok {
		zlog.Error("[HTTP] Not Found Hostname: ",hostname)
		conn.Write([]byte(HttpStatus(503)))
		conn.Write([]byte("\n"))
		conn.Write([]byte(Page503))
		conn.Close()
		return
	}

	Setting.Rules.RLock()
	r := Setting.Config.Rules[i]
	Setting.Rules.RUnlock()

	if r.Status != "Active" && r.Status != "Created" {
		limitWrite(conn, r.UserID, []byte(HttpStatus(503)))
		limitWrite(conn, r.UserID, []byte("\n"))
		limitWrite(conn, r.UserID, []byte(Page503))
		conn.Close()
		return
	}

	proxy, error := net.Dial("tcp", ParseForward(r))
	if error != nil {
		limitWrite(conn, r.UserID, []byte(HttpStatus(522)))
		limitWrite(conn, r.UserID, []byte("\n"))
		limitWrite(conn, r.UserID, []byte(Page522))
		conn.Close()
		return
	}

	if r.ProxyProtocolVersion != 0 {
		header, err := proxyprotocol.HeaderProxyFromAddrs(byte(r.ProxyProtocolVersion), conn.RemoteAddr(), conn.LocalAddr()).Format()
		if err == nil {
			limitWrite(proxy, r.UserID, header)
		}
	}

	for element := readLines.Front(); element != nil; element = element.Next() {
		line := element.Value.(string)
		limitWrite(proxy, r.UserID, []byte(line))
		limitWrite(proxy, r.UserID, []byte("\n"))
	}

	go copyIO(conn, proxy, r)
	go copyIO(proxy, conn, r)
}

func ParseAddrToIP(addr string) string {
	var str string
	arr := strings.Split(addr, ":")
	for i := 0; i < (len(arr) - 1); i++ {
		if i != 0 {
			str = str + ":" + arr[i]
		} else {
			str = str + arr[i]
		}
	}
	return str
}

func ParseHostToName(host string) string {
	if strings.Index(host, ":") == -1 {
		return strings.ToLower(host)
	} else {
		return strings.ToLower(strings.Split(host, ":")[0])
	}
}
