package main

import (
	"bufio"
	"container/list"
	"github.com/CoiaPrant/zlog"
	"net"
	"strings"

	proxyprotocol "github.com/pires/go-proxyproto"
)

func HttpInit(port string) {
	zlog.Info("[HTTP] Listening ", port)
	l, err := net.Listen("tcp", ":"+port)
	if err != nil {
		zlog.Error("[HTTP] Listen failed , Error: ", err)
		return
	}

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

func LoadHttpRules(i string, r Rule) {
	if Shared.HTTP.Has(strings.ToLower(r.Listen)) {
		return
	}

	Shared.HTTP.Set(strings.ToLower(r.Listen), i)
	zlog.Info("Loaded [", r.UserID, "][", i, "] (HTTP)", r.Listen, " => ", ParseForward(r))
}

func DeleteHttpRules(i string, r Rule) {
	Shared.HTTP.Remove(strings.ToLower(r.Listen))
	zlog.Info("Deleted [", r.UserID, "][", i, "] (HTTP)", r.Listen, " => ", ParseForward(r))
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
		conn.Write([]byte(HttpStatus(503)))
		conn.Write([]byte("\n"))
		conn.Write([]byte(Page503))
		conn.Close()
		return
	}

	value, _ := Shared.HTTP.Get(hostname)
	i, ok := value.(string)
	if !ok {
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
		limitWrite(conn, r, []byte(HttpStatus(503)))
		limitWrite(conn, r, []byte("\n"))
		limitWrite(conn, r, []byte(Page503))
		conn.Close()
		return
	}

	proxy, error := net.Dial("tcp", ParseForward(r))
	if error != nil {
		limitWrite(conn, r, []byte(HttpStatus(522)))
		limitWrite(conn, r, []byte("\n"))
		limitWrite(conn, r, []byte(Page522))
		conn.Close()
		return
	}

	if r.ProxyProtocolVersion != 0 {
		header, err := proxyprotocol.HeaderProxyFromAddrs(byte(r.ProxyProtocolVersion), conn.LocalAddr(), conn.RemoteAddr()).Format()
		if err == nil {
			limitWrite(proxy, r, header)
		}
	}

	for element := readLines.Front(); element != nil; element = element.Next() {
		line := element.Value.(string)
		limitWrite(proxy, r, []byte(line))
		limitWrite(proxy, r, []byte("\n"))
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
