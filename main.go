package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/CoiaPrant/zlog"
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
	Used  *int64
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
		zlog.Fatal("Unable to get local configuration file. (i/o Error): " + err.Error())
		return
	}

	err = json.Unmarshal(api_conf, &API)
	if err != nil {
		zlog.Fatal("Unable to get local configuration file. (Parse Error): " + err.Error())
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
		for index, value := range NewConfig.Users {
			if _, ok := Setting.Config.Users[index]; !ok {
				Setting.Config.Users[index] = value
			}
		}
		Setting.Users.Unlock()

		Setting.Rules.Lock()
		for index, value := range NewConfig.Rules {
			Setting.Config.Rules[index] = value
		}
		Setting.Rules.Unlock()

		for index, rule := range NewConfig.Rules {
			switch rule.Status {
			case "Created":
				LoadNewRules(index, rule)
			case "Deleted":
				DeleteRules(index, rule)
			}
		}
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
	jsonData, err := json.Marshal(map[string]interface{}{
		"Action":  "GetConfig",
		"NodeID":  API.NodeID,
		"Token":   md5_encode(API.APIToken),
		"Version": version,
	})

	if err != nil {
		zlog.Fatal("Error submitting information. (Parse Error): " + err.Error())
		return
	}

	status, data, err := sendRequest(API.APIAddr, jsonData, nil, "POST")
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

	jsonData, err := json.Marshal(map[string]interface{}{
		"Action":  "UpdateInfo",
		"NodeID":  API.NodeID,
		"Token":   md5_encode(API.APIToken),
		"Users":   NowConfig.Users,
		"Version": version,
	})

	if err != nil {
		Setting.Users.Unlock()
		zlog.Error("Error submitting information. (Parse Error): " + err.Error())
		return
	}

	status, confF, err := sendRequest(API.APIAddr, jsonData, nil, "POST")
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
		switch rule.Status {
		case "Created":
			LoadNewRules(index, rule)
		case "Deleted":
			DeleteRules(index, rule)
		}
	}
	zlog.Success("Scheduled task update Completed")
}

func saveConfig() {
	defer Setting.Rules.Unlock()
	defer Setting.Users.Unlock()
	CloseAllListener()
	Setting.Rules.Lock()
	time.Sleep(time.Second)
	Setting.Users.Lock()

	jsonData, err := json.Marshal(map[string]interface{}{
		"Action":  "UpdateInfo",
		"NodeID":  API.NodeID,
		"Token":   md5_encode(API.APIToken),
		"Users":   Setting.Config.Users,
		"Version": version,
	})

	if err != nil {
		zlog.Error("Error submitting information. (Parse Error): " + err.Error())
		return
	}

	status, data, err := sendRequest(API.APIAddr, jsonData, nil, "POST")
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
		sendRequest(API.APIAddr, jsonData, nil, "POST")
	}
}

func copyIO(src, dest net.Conn, r Rule) (int64, error) {
	defer src.Close()
	defer dest.Close()

	var b int64
	var err error

	if r.Speed == 0 {
		b, err = io.Copy(dest, src)
	} else {
		bucket := ratelimit.New(r.Speed * 128 * 1024)
		b, err = io.Copy(ratelimit.Writer(dest, bucket), src)
	}

	go func() {
		Setting.Users.Lock()
		*Setting.Config.Users[r.UserID].Used += b
		NowUser := Setting.Config.Users[r.UserID]
		Setting.Users.Unlock()

		if NowUser.Quota <= *NowUser.Used {
			go updateConfig()
		}
	}()
	return b, err
}

func limitWrite(dest net.Conn, r Rule, buf []byte) (int, error) {
	var b int
	var err error

	if r.Speed == 0 {
		b, err = dest.Write(buf)
	} else {
		bucket := ratelimit.New(r.Speed * 128 * 1024)
		b, err = ratelimit.Writer(dest, bucket).Write(buf)
	}
	go func() {
		Setting.Users.Lock()
		*Setting.Config.Users[r.UserID].Used += int64(b)
		NowUser := Setting.Config.Users[r.UserID]
		Setting.Users.Unlock()

		if NowUser.Quota <= *NowUser.Used {
			go updateConfig()
		}
	}()
	return b, err
}

func ParseForward(r Rule) string {
	if strings.Count(r.RemoteHost, ":") == 1 {
		return "[" + r.RemoteHost + "]:" + strconv.Itoa(r.RemotePort)
	}

	return r.RemoteHost + ":" + strconv.Itoa(r.RemotePort)
}
