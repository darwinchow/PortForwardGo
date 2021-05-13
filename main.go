package main

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"github.com/CoiaPrant/zlog"
	"io"
	"io/ioutil"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gitee.com/kzquu/wego/util/ratelimit"
	cmap "github.com/orcaman/concurrent-map"
	kcp "github.com/xtaci/kcp-go"
)

var API APIConfig
var Setting FullConfig
var Shared SharePort
var version string

var ConfigFile string
var LogFile string
var certFile string
var keyFile string

type FullConfig struct {
	Listener cmap.ConcurrentMap
	Config   Config
	Rules    sync.RWMutex
	Users    sync.Mutex
}

type SharePort struct {
	HTTP  cmap.ConcurrentMap
	HTTPS cmap.ConcurrentMap
}

type Config struct {
	UpdateInfoCycle int
	EnableAPI       bool
	APIPort         string
	Listen          map[string]Listen
	Rules           map[string]Rule
	Users           map[string]User
}

type Listen struct {
	Enable bool
	Port   string
}

type User struct {
	Quota int64
	Used  int64
}

type Rule struct {
	Status               string
	UserID               string
	Protocol             string
	Speed                int64
	Listen               string
	RemoteHost           string
	RemotePort           int
	ProxyProtocolVersion int
}

type APIConfig struct {
	APIAddr  string
	APIToken string
	NodeID   int
}

type POSTData struct {
	Action  string `json:"Action"`
	NodeID  int    `json:"NodeID"`
	Token   string `json:"Token"`
	Info    Config `json:"Info"`
	Version string `json:"Version"`
}

func main() {
	{
		Setting.Listener = cmap.New()
		Shared.HTTP = cmap.New()
		Shared.HTTPS = cmap.New()
	}

	{
		flag.StringVar(&ConfigFile, "config", "config.json", "The config file location")
		flag.StringVar(&certFile, "certfile", "public.pem", "The ssl cert file location")
		flag.StringVar(&keyFile, "keyfile", "private.key.", "The ssl key file location")
		flag.StringVar(&LogFile, "log", "", "The log file location")
		help := flag.Bool("h", false, "Show help")
		flag.Parse()

		if *help {
			flag.PrintDefaults()
			os.Exit(0)
		}
	}

	{
		Setting.Listener = cmap.New()
		Shared.HTTP = cmap.New()
		Shared.HTTPS = cmap.New()
	}

	if LogFile != "" {
		os.Remove(LogFile)
		logfile_writer, err := os.OpenFile(LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			zlog.SetOutput(logfile_writer)
			zlog.Info("Log file location: ", LogFile)
		}
	}

	_, err := os.Stat(certFile)
	if err != nil {
		CreateTLSFile(certFile, keyFile)
	}

	_, err = os.Stat(keyFile)
	if err != nil {
		CreateTLSFile(certFile, keyFile)
	}

	zlog.Info("Node Version: ", version)

	api_conf, err := ioutil.ReadFile(ConfigFile)
	if err != nil {
		zlog.Fatal("Unable to get local configuration file. (i/o Error) " + err.Error())
		return
	}

	err = json.Unmarshal(api_conf, &API)
	if err != nil {
		zlog.Fatal("Unable to get local configuration file. (Parse Error) " + err.Error())
		return
	}

	zlog.Info("API URL: ", API.APIAddr)
	getConfig()

	go func() {
		if Setting.Config.EnableAPI == true {
			zlog.Info("[HTTP API] Listening ", Setting.Config.APIPort, " Path: /", md5_encode(API.APIToken), " Method:POST")
			route := http.NewServeMux()
			route.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(404)
				io.WriteString(w, Page404)
				return
			})
			route.HandleFunc("/"+md5_encode(API.APIToken), NewAPIConnect)
			err := http.ListenAndServe(":"+Setting.Config.APIPort, route)
			if err != nil {
				zlog.Error("[HTTP API] ", err)
			}
		}
	}()

	go func() {
		for {
			saveInterval := time.Duration(Setting.Config.UpdateInfoCycle) * time.Second
			time.Sleep(saveInterval)
			updateConfig()
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	<-quit
	saveConfig()
	zlog.PrintText("Exiting")
}

func NewAPIConnect(w http.ResponseWriter, r *http.Request) {
	var NewConfig Config
	if r.Method != "POST" {
		w.WriteHeader(403)
		io.WriteString(w, "Unsupport Method.")
		zlog.Error("[API] Unsupport Method. Client IP: " + r.RemoteAddr + " URI: " + r.RequestURI)
		return
	}

	postdata, _ := ioutil.ReadAll(r.Body)
	err := json.Unmarshal(postdata, &NewConfig)
	if err != nil {
		w.WriteHeader(400)
		io.WriteString(w, fmt.Sprintln(err))
		zlog.Error("[API] Json Parse Error(" + err.Error() + "). Client IP: " + r.RemoteAddr + " URI: " + r.RequestURI)
		return
	}

	w.WriteHeader(200)

	jsonData, _ := json.Marshal(map[string]interface{}{
		"Result": true,
	})
	w.Write(jsonData)

	zlog.Success("[API] Client IP: " + r.RemoteAddr + " URI: " + r.RequestURI)

	go func() {
		if Setting.Config.Rules == nil {
			Setting.Config.Rules = make(map[string]Rule)
		}

		if Setting.Config.Users == nil {
			Setting.Config.Users = make(map[string]User)
		}

		Setting.Users.Lock()
		for index, v := range NewConfig.Users {
			if _, ok := Setting.Config.Users[index]; !ok {
				Setting.Config.Users[index] = v
			}
		}
		Setting.Users.Unlock()

		Setting.Rules.Lock()
		for index, value := range NewConfig.Rules {
			if value.Status == "Deleted" {
				DeleteRules(index, value)
				continue
			} else if value.Status == "Created" {
				Setting.Config.Rules[index] = value
				LoadNewRules(index, value)
				continue
			} else {
				Setting.Config.Rules[index] = NewConfig.Rules[index]
				continue
			}
		}
		Setting.Rules.Unlock()

	}()
	return
}

func LoadListen() {
	for name, value := range Setting.Config.Listen {
		if value.Enable {
			switch name {
			case "Http":
				go HttpInit(value.Port)
			case "Https":
				go HttpsInit(value.Port)
			case "Http_2":
				go HttpInit(value.Port)
			case "Https_2":
				go HttpsInit(value.Port)
			}
		}
	}
}

func CloseAllListener() {
	for item := range Setting.Listener.IterBuffered() {
		if ln, ok := item.Val.(*net.TCPListener); ok {
			ln.Close()
		}
		if ln, ok := item.Val.(*net.UDPConn); ok {
			ln.Close()
		}
		if ln, ok := item.Val.(*kcp.Listener); ok {
			ln.Close()
		}
	}
}

func LoadNewRules(i string, r Rule) {
	switch r.Protocol {
	case "tcp":
		go LoadTCPRules(i, r)
	case "udp":
		go LoadUDPRules(i, r)
	case "kcp":
		go LoadKCPRules(i, r)
	case "http":
		go LoadHttpRules(i, r)
	case "https":
		go LoadHttpsRules(i, r)
	case "ws":
		go LoadWSRules(i, r)
	case "wsc":
		go LoadWSCRules(i, r)
	case "wss":
		go LoadWSSRules(i, r)
	case "wssc":
		go LoadWSSCRules(i, r)
	}
}

func DeleteRules(i string, r Rule) {
	switch r.Protocol {
	case "tcp":
		go DeleteTCPRules(i, r)
	case "udp":
		go DeleteUDPRules(i, r)
	case "kcp":
		go DeleteKCPRules(i, r)
	case "http":
		go DeleteHttpRules(i, r)
	case "https":
		go DeleteHttpsRules(i, r)
	case "ws":
		go DeleteWSRules(i, r)
	case "wsc":
		go DeleteWSCRules(i, r)
	case "wss":
		go DeleteWSSRules(i, r)
	case "wssc":
		go DeleteWSSCRules(i, r)
	}

	delete(Setting.Config.Rules, i)
}

func getConfig() {
	var NewConfig Config
	jsonData, err := json.Marshal(&POSTData{
		Action:  "GetConfig",
		NodeID:  API.NodeID,
		Token:   md5_encode(API.APIToken),
		Version: version,
	})

	if err != nil {
		zlog.Fatal("Error submitting information. (Parse Error): " + err.Error())
		return
	}

	status, data, err := sendRequest(API.APIAddr, bytes.NewReader(jsonData), nil, "POST")
	if status == 503 {
		zlog.Fatal("The remote server returned an error message: ", string(data))
		return
	}

	if err != nil {
		zlog.Fatal("Unable to get configuration file. (NetWork Error): " + err.Error())
		return
	}

	err = json.Unmarshal(data, &NewConfig)
	if err != nil {
		zlog.Fatal("Unable to get configuration file. (Parse Error): " + err.Error())
		return
	}

	Setting.Config = NewConfig
	zlog.Info("Update Cycle: ", Setting.Config.UpdateInfoCycle, " seconds")
	LoadListen()

	for index, value := range NewConfig.Rules {
		LoadNewRules(index, value)
	}
}

func updateConfig() {
	var NewConfig Config

	Setting.Users.Lock()

	Setting.Rules.RLock()
	NowConfig := Setting.Config
	Setting.Rules.RUnlock()

	jsonData, err := json.Marshal(&POSTData{
		Action:  "UpdateInfo",
		NodeID:  API.NodeID,
		Token:   md5_encode(API.APIToken),
		Info:    NowConfig,
		Version: version,
	})

	if err != nil {
		Setting.Users.Unlock()
		zlog.Error("Error submitting information. (Parse Error): " + err.Error())
		return
	}

	status, confF, err := sendRequest(API.APIAddr, bytes.NewReader(jsonData), nil, "POST")
	if status == 503 {
		Setting.Users.Unlock()
		zlog.Error("Scheduled task update error,The remote server returned an error message: ", string(confF))
		return
	}

	if err != nil {
		Setting.Users.Unlock()
		zlog.Error("Scheduled task update network error: ", err)
		return
	}

	err = json.Unmarshal(confF, &NewConfig)
	if err != nil {
		Setting.Users.Unlock()
		zlog.Error("Scheduled task update parse error: " + err.Error())
		return
	}

	Setting.Rules.Lock()
	Setting.Config = NewConfig
	Setting.Rules.Unlock()
	Setting.Users.Unlock()

	for index, rule := range NewConfig.Rules {
		if rule.Status == "Deleted" {
			DeleteRules(index, rule)
			continue
		} else if rule.Status == "Created" {
			LoadNewRules(index, rule)
			continue
		}
	}
	zlog.Success("Scheduled task update Completed")
}

func saveConfig() {
	defer Setting.Rules.Unlock()
	defer Setting.Users.Unlock()
	CloseAllListener()
	Setting.Rules.Lock()
	Setting.Users.Lock()

	jsonData, err := json.Marshal(&POSTData{
		Action:  "SaveConfig",
		NodeID:  API.NodeID,
		Token:   md5_encode(API.APIToken),
		Info:    Setting.Config,
		Version: version,
	})

	if err != nil {
		zlog.Error("Error submitting information. (Parse Error): " + err.Error())
		return
	}

	status, data, err := sendRequest(API.APIAddr, bytes.NewReader(jsonData), nil, "POST")
	if status == 503 {
		zlog.Error("Save config error,The remote server returned an error message: ", string(data))
		return
	}
	if err != nil {
		zlog.Error("Save config error: ", err)
		return
	}

	zlog.Success("Save config Completed")
}

func SendListenError(i string) {
	jsonData, err := json.Marshal(map[string]interface{}{
		"Action":  "Error",
		"NodeID":  API.NodeID,
		"Token":   md5_encode(API.APIToken),
		"Version": version,
		"RuleID":  i,
	})

	if err == nil {
		sendRequest(API.APIAddr, bytes.NewReader(jsonData), nil, "POST")
	}
}

func sendRequest(url string, body io.Reader, addHeaders map[string]string, method string) (statuscode int, resp []byte, err error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/86.0.4240.198 Safari/537.36")

	if len(addHeaders) > 0 {
		for k, v := range addHeaders {
			req.Header.Add(k, v)
		}
	}

	client := &http.Client{}
	response, err := client.Do(req)
	if err != nil {
		return
	}
	defer response.Body.Close()

	statuscode = response.StatusCode
	resp, err = ioutil.ReadAll(response.Body)
	return
}

func md5_encode(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func copyIO(src, dest net.Conn, r Rule) {
	defer src.Close()
	defer dest.Close()

	var b int64

	if r.Speed != 0 {
		bucket := ratelimit.New(r.Speed * 128 * 1024)
		b, _ = io.Copy(ratelimit.Writer(dest, bucket), src)
	} else {
		b, _ = io.Copy(dest, src)
	}

	Setting.Users.Lock()

	NowUser := Setting.Config.Users[r.UserID]
	NowUser.Used += b
	Setting.Config.Users[r.UserID] = NowUser

	Setting.Users.Unlock()

	if NowUser.Quota <= NowUser.Used {
		go updateConfig()
	}
}

func limitWrite(dest net.Conn, userid string, buf []byte) {
	var r int

	r, _ = dest.Write(buf)

	go func() {
		Setting.Users.Lock()

		NowUser := Setting.Config.Users[userid]
		NowUser.Used += int64(r)
		Setting.Config.Users[userid] = NowUser

		Setting.Users.Unlock()

		if NowUser.Quota <= NowUser.Used {
			go updateConfig()
		}
	}()
}

func CreateTLSFile(certFile, keyFile string) {
	var ip string
	os.Remove(certFile)
	os.Remove(keyFile)
	max := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, _ := rand.Int(rand.Reader, max)
	subject := pkix.Name{
		Country:            []string{"US"},
		Province:           []string{"WDC"},
		Organization:       []string{"Microsoft Corporation"},
		OrganizationalUnit: []string{"Microsoft Corporation"},
		CommonName:         "www.microstft.com",
	}

	_, resp, err := sendRequest("https://api.ip.sb/ip", nil, nil, "GET")
	if err == nil {
		ip = string(resp)
		ip = strings.Replace(ip, "\n", "", -1)
	} else {
		ip = "127.0.0.1"
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      subject,
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP(ip)},
	}

	pk, _ := rsa.GenerateKey(rand.Reader, 2048)

	derBytes, _ := x509.CreateCertificate(rand.Reader, &template, &template, &pk.PublicKey, pk)
	certOut, _ := os.Create(certFile)
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	certOut.Close()

	keyOut, _ := os.Create(keyFile)
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(pk)})
	keyOut.Close()
	zlog.Success("Created the ssl certfile,location: ", certFile)
	zlog.Success("Created the ssl keyfile,location: ", keyFile)
}

func ParseForward(r Rule) string {
	if strings.Count(r.RemoteHost, ":") == 1 {
		return "[" + r.RemoteHost + "]:" + strconv.Itoa(r.RemotePort)
	}

	return r.RemoteHost + ":" + strconv.Itoa(r.RemotePort)
}
