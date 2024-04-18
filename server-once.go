package main

import (
	"context"
	"crypto/tls"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"sync"

	"github.com/armon/go-socks5"
	"github.com/google/uuid"
	"golang.org/x/net/http2"
)

// ConnectionStore 存储从 SOCKS5 收集的数据
var ConnectionStore = struct {
	sync.RWMutex
	Data        map[string][]byte
	Connections map[string]net.Conn
}{
	Data:        make(map[string][]byte),
	Connections: make(map[string]net.Conn),
}

// recordingConn 包装 net.Conn 以捕获和记录数据，并关联 UUID
type recordingConn struct {
	net.Conn
	ID string
}

func (c *recordingConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		ConnectionStore.Lock()
		ConnectionStore.Data[c.ID] = append(ConnectionStore.Data[c.ID], b[:n]...)
		ConnectionStore.Connections[c.ID] = c.Conn
		ConnectionStore.Unlock()
	}
	return n, err
}

func (c *recordingConn) Write(b []byte) (int, error) {
	return c.Conn.Write(b) // 实际上只是代理写操作
}

// setupSocks5Server 设置并启动 SOCKS5 服务器
func setupSocks5Server() {
	conf := &socks5.Config{
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			baseConn, err := net.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			reqID := uuid.New().String()
			println(reqID, ":", baseConn)
			return &recordingConn{Conn: baseConn, ID: reqID}, nil
		},
	}
	server, err := socks5.New(conf)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		log.Println("Starting SOCKS5 server on :1080")
		if err := server.ListenAndServe("tcp", "0.0.0.0:1080"); err != nil {
			log.Fatal(err)
		}
	}()
}

// handleTunnelRequest 处理通过 HTTP/2 来的隧道请求
func handleTunnelRequest(w http.ResponseWriter, r *http.Request) {
	ConnectionStore.RLock()
	// 检查是否有可用的连接
	if len(ConnectionStore.Connections) == 0 {
		ConnectionStore.RUnlock()
		http.Error(w, "No available connections", http.StatusServiceUnavailable)
		return
	}

	// 从连接池中随机选择一个连接
	var chosenID string
	var chosenData []byte
	i := rand.Intn(len(ConnectionStore.Connections))
	for id := range ConnectionStore.Connections {
		if i == 0 {
			chosenID = id
			chosenData = ConnectionStore.Data[id]
			break
		}
		i--
	}
	ConnectionStore.RUnlock()

	// 设置HTTP头部中的X-Request-ID
	w.Header().Set("X-Request-ID", chosenID)
	w.Header().Set("Content-Type", "application/octet-stream")
	println("Sending to client...")
	if _, err := w.Write(chosenData); err != nil {
		log.Printf("Error sending data to client: %v", err)
		http.Error(w, "Failed to send data", http.StatusInternalServerError)
		return
	}
}

func handleTunnelResponse(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		http.Error(w, "Missing X-Request-ID", http.StatusBadRequest)
		return
	}

	ConnectionStore.RLock()
	conn, exists := ConnectionStore.Connections[requestID]
	ConnectionStore.RUnlock()
	if !exists {
		http.Error(w, "Request ID not found", http.StatusNotFound)
		return
	}
	println("recive result, sending...")
	_, err = conn.Write(body)
	if err != nil {
		log.Printf("Error sending response back to client: %v", err)
		http.Error(w, "Failed to send response", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully handled request for %s", requestID)
	w.WriteHeader(http.StatusOK)
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/tunnel", handleTunnelRequest)
	mux.HandleFunc("/reserve", handleTunnelResponse)

	server := &http.Server{
		Addr:      ":443",
		Handler:   mux,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	http2.ConfigureServer(server, &http2.Server{})

	log.Println("Starting HTTP/2 server on :443")
	setupSocks5Server()
	log.Fatal(server.ListenAndServeTLS("cert.crt", "key.key"))
}
