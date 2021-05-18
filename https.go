package main

import (
	"github.com/CoiaPrant/zlog"
	"net"
	"strings"

	proxyprotocol "github.com/pires/go-proxyproto"
)

func HttpsInit(port string) {
	zlog.Info("[HTTPS] Listening ", port)
	l, err := net.Listen("tcp", ":"+port)
	if err != nil {
		zlog.Error("[HTTPS] Listen failed , Error: ", err)
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

		go https_handle(c)
	}
}

func LoadHttpsRules(i string, r Rule) {
	if Shared.HTTPS.Has(strings.ToLower(r.Listen)) {
		return
	}
	Shared.HTTPS.Set(strings.ToLower(r.Listen), i)

	zlog.Info("Loaded [", r.UserID, "][", i, "] (HTTPS)", r.Listen, " => ", ParseForward(r))
}

func DeleteHttpsRules(i string, r Rule) {
	Shared.HTTPS.Remove(strings.ToLower(r.Listen))
	zlog.Info("Deleted [", r.UserID, "][", i, "] (HTTPS)", r.Listen, " => ", ParseForward(r))
}

func https_handle(conn net.Conn) {
	firstByte := make([]byte, 1)
	_, err := conn.Read(firstByte)
	if err != nil {
		conn.Close()
		return
	}

	if firstByte[0] != 0x16 {
		conn.Close()
		return
	}

	versionBytes := make([]byte, 2)
	_, err = conn.Read(versionBytes)
	if err != nil {
		conn.Close()
		return
	}
	if versionBytes[0] < 3 || (versionBytes[0] == 3 && versionBytes[1] < 1) {
		conn.Close()
		return
	}

	restLengthBytes := make([]byte, 2)
	_, err = conn.Read(restLengthBytes)
	if err != nil {
		conn.Close()
		return
	}
	restLength := (int(restLengthBytes[0]) << 8) + int(restLengthBytes[1])

	rest := make([]byte, restLength)
	_, err = conn.Read(rest)
	if err != nil {
		conn.Close()
		return
	}

	current := 0
	if len(rest) == 0 {
		conn.Close()
		return
	}
	handshakeType := rest[0]
	current += 1
	if handshakeType != 0x1 {
		conn.Close()
		return
	}

	current += 3
	current += 2
	current += 4 + 28
	sessionIDLength := int(rest[current])
	current += 1
	current += sessionIDLength

	cipherSuiteLength := (int(rest[current]) << 8) + int(rest[current+1])
	current += 2
	current += cipherSuiteLength

	compressionMethodLength := int(rest[current])
	current += 1
	current += compressionMethodLength

	if current > restLength {
		conn.Close()
		return
	}

	current += 2

	hostname := ""
	for current < restLength && hostname == "" {
		extensionType := (int(rest[current]) << 8) + int(rest[current+1])
		current += 2

		extensionDataLength := (int(rest[current]) << 8) + int(rest[current+1])
		current += 2

		if extensionType == 0 {
			current += 2

			nameType := rest[current]
			current += 1
			if nameType != 0 {
				conn.Close()
				return
			}
			nameLen := (int(rest[current]) << 8) + int(rest[current+1])
			current += 2
			hostname = strings.ToLower(string(rest[current : current+nameLen]))
		}

		current += extensionDataLength
	}

	if hostname == "" {
		conn.Close()
		return
	}

	value, _ := Shared.HTTPS.Get(hostname)
	i, ok := value.(string)
	if !ok {
		conn.Close()
		return
	}

	Setting.Rules.RLock()
	r := Setting.Config.Rules[i]
	Setting.Rules.RUnlock()

	if r.Status != "Active" && r.Status != "Created" {
		conn.Close()
		return
	}

	proxy, error := net.Dial("tcp", ParseForward(r))
	if error != nil {
		conn.Close()
		return
	}

	if r.ProxyProtocolVersion != 0 {
		header, err := proxyprotocol.HeaderProxyFromAddrs(byte(r.ProxyProtocolVersion), conn.RemoteAddr(), conn.LocalAddr()).Format()
		if err == nil {
			limitWrite(proxy, r, header)
		}
	}

	limitWrite(proxy, r, firstByte)
	limitWrite(proxy, r, versionBytes)
	limitWrite(proxy, r, restLengthBytes)
	limitWrite(proxy, r, rest)

	go copyIO(conn, proxy, r)
	go copyIO(proxy, conn, r)
}
