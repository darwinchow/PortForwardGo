package main

import (
	"github.com/CoiaPrant/zlog"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
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
	kcp "github.com/xtaci/kcp-go"
)

var Setting CSafeRule
var version string

var ConfigFile string
var LogFile string

type CSafeRule struct {
	Listener Listener
	Config   Config
	Rules    sync.RWMutex
	Users    sync.Mutex
}

type Listener struct {
	Turn        sync.RWMutex
	HTTPServer  net.Listener
	HTTPSServer net.Listener
	TCP         map[string]*net.TCPListener
	UDP         map[string]*net.UDPConn
	KCP         map[string]*kcp.Listener
	HTTP        map[string]string
	HTTPS       map[string]string
	WS          map[string]*net.TCPListener
	WSC         map[string]*net.TCPListener
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

var apic APIConfig

func main() {
	{
		flag.StringVar(&ConfigFile, "config", "config.json", "The config file location.")
		flag.StringVar(&LogFile, "log", "", "The log file location.")
		help := flag.Bool("h", false, "Show help")
		flag.Parse()

		if *help {
			flag.PrintDefaults()
			os.Exit(0)
		}
	}

	{
		Setting.Listener.TCP = make(map[string]*net.TCPListener)
		Setting.Listener.UDP = make(map[string]*net.UDPConn)
		Setting.Listener.KCP = make(map[string]*kcp.Listener)
		Setting.Listener.HTTP = make(map[string]string)
		Setting.Listener.HTTPS = make(map[string]string)
		Setting.Listener.WS = make(map[string]*net.TCPListener)
		Setting.Listener.WSC = make(map[string]*net.TCPListener)
	}

	if LogFile != "" {
		os.Remove(LogFile)
		logfile_writer, err := os.OpenFile(LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			zlog.SetOutput(logfile_writer)
			zlog.Info("Log file location: ", LogFile)
		}
	}
	zlog.Info("Node Version: ", version)

	apif, err := ioutil.ReadFile(ConfigFile)
	if err != nil {
		zlog.Fatal("Cannot read the config file. (io Error) " + err.Error())
	}

	err = json.Unmarshal(apif, &apic)
	if err != nil {
		zlog.Fatal("Cannot read the config file. (Parse Error) " + err.Error())
	}

	zlog.Info("API URL: ", apic.APIAddr)
	getConfig()

	go func() {
		if Setting.Config.EnableAPI == true {
			zlog.Info("[HTTP API] Listening ", Setting.Config.APIPort, " Path: /", md5_encode(apic.APIToken), " Method:POST")
			route := http.NewServeMux()
			route.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(404)
				io.WriteString(w, Page404)
				return
			})
			route.HandleFunc("/"+md5_encode(apic.APIToken), NewAPIConnect)
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
	io.WriteString(w, "Success")
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
		for index, _ := range NewConfig.Rules {
			if NewConfig.Rules[index].Status == "Deleted" {
				DeleteRules(index)
				continue
			} else if NewConfig.Rules[index].Status == "Created" {
				Setting.Config.Rules[index] = NewConfig.Rules[index]
				 LoadNewRules(index)
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
				go HttpInit()
			case "Https":
				go HttpsInit()
			}
		}
	}
}
func CloseAllListener() {
	Setting.Listener.Turn.Lock()
	if Setting.Listener.HTTPServer != nil {
		Setting.Listener.HTTPServer.Close()
	}
	if Setting.Listener.HTTPSServer != nil {
		Setting.Listener.HTTPSServer.Close()
	}
	for _, ln := range Setting.Listener.TCP {
		ln.Close()
	}
	for _, ln := range Setting.Listener.UDP {
		ln.Close()
	}
	for _, ln := range Setting.Listener.KCP {
		ln.Close()
	}
	for _, ln := range Setting.Listener.WS {
		ln.Close()
	}
	for _, ln := range Setting.Listener.WSC {
		ln.Close()
	}
	Setting.Listener.Turn.Unlock()
}

func DeleteRules(i string) {
	if _, ok := Setting.Config.Rules[i]; !ok {
		return
	}

	Protocol := Setting.Config.Rules[i].Protocol
	switch Protocol {
	case "tcp":
		go DeleteTCPRules(i)
	case "udp":
		go DeleteUDPRules(i)
	case "kcp":
		go DeleteKCPRules(i)
	case "http":
		go DeleteHttpRules(i)
	case "https":
		go DeleteHttpsRules(i)
	case "ws":
		go DeleteWSRules(i)
	case "wsc":
		go DeleteWSCRules(i)
	}
}

func LoadNewRules(i string) {
	Protocol := Setting.Config.Rules[i].Protocol

	switch Protocol {
	case "tcp":
		go LoadTCPRules(i)
	case "udp":
		go LoadUDPRules(i)
	case "kcp":
		go LoadKCPRules(i)
	case "http":
		go LoadHttpRules(i)
	case "https":
		go LoadHttpsRules(i)
	case "ws":
		go LoadWSRules(i)
	case "wsc":
		go LoadWSCRules(i)
	}
}

func getConfig() {
	var NewConfig Config
	jsonData, _ := json.Marshal(map[string]interface{}{
		"Action":  "GetConfig",
		"NodeID":  apic.NodeID,
		"Token":   md5_encode(apic.APIToken),
		"Version": version,
	})
	status, confF, err := sendRequest(apic.APIAddr, bytes.NewReader(jsonData), nil, "POST")
	if status == 503 {
		zlog.Fatal("The remote server returned an error message: ", string(confF))
		return
	}

	if err != nil {
		zlog.Fatal("Cannot read the online config file. (NetWork Error) " + err.Error())
		return
	}

	err = json.Unmarshal(confF, &NewConfig)
	if err != nil {
		zlog.Fatal("Cannot read the port forward config file. (Parse Error) " + err.Error())
		return
	}
	Setting.Config = NewConfig
	zlog.Info("Update Cycle: ", Setting.Config.UpdateInfoCycle, " seconds")
	LoadListen()

	for index, _ := range NewConfig.Rules {
		LoadNewRules(index)
	}
}

func updateConfig() {
	var NewConfig Config

	Setting.Users.Lock()

	Setting.Rules.RLock()
	NowConfig := Setting.Config
	Setting.Rules.RUnlock()

	jsonData, _ := json.Marshal(map[string]interface{}{
		"Action":  "UpdateInfo",
		"NodeID":  apic.NodeID,
		"Token":   md5_encode(apic.APIToken),
		"Info":    &NowConfig,
		"Version": version,
	})

	status, confF, err := sendRequest(apic.APIAddr, bytes.NewReader(jsonData), nil, "POST")
	if status == 503 {
		Setting.Users.Unlock()
		zlog.Error("Scheduled task update error,The remote server returned an error message: ", string(confF))
		return
	}
	if err != nil {
		Setting.Users.Unlock()
		zlog.Error("Scheduled task update error: ", err)
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

	for index, rule := range Setting.Config.Rules {
		if rule.Status == "Deleted" {
			 DeleteRules(index)
			continue
		} else if rule.Status == "Created" {
			 LoadNewRules(index)
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

	jsonData, _ := json.Marshal(map[string]interface{}{
		"Action":  "SaveConfig",
		"NodeID":  apic.NodeID,
		"Token":   md5_encode(apic.APIToken),
		"Info":    &Setting.Config,
		"Version": version,
	})
	status, confF, err := sendRequest(apic.APIAddr, bytes.NewReader(jsonData), nil, "POST")
	if status == 503 {
		zlog.Error("Save config error,The remote server returned an error message , message: ", string(confF))
		return
	}
	if err != nil {
		zlog.Error("Save config error: ", err)
		return
	}

	zlog.Success("Save config Completed")
}

func SendListenError(i string) {
	jsonData, _ := json.Marshal(map[string]interface{}{
		"Action":  "Error",
		"NodeID":  apic.NodeID,
		"Token":   md5_encode(apic.APIToken),
		"Version": version,
		"RuleID":  i,
	})
	sendRequest(apic.APIAddr, bytes.NewReader(jsonData), nil, "POST")
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

func ParseForward(r Rule) string {
	if strings.Count(r.RemoteHost, ":") == 1 {
		return "[" + r.RemoteHost + "]:" + strconv.Itoa(r.RemotePort)
	}

	return r.RemoteHost + ":" + strconv.Itoa(r.RemotePort)
}
