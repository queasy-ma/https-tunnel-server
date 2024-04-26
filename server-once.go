package main

import (
	"context"
	"crypto/tls"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

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

// virtualConn 是一个虚拟的net.Conn，不实际发送数据到网络
type virtualConn struct {
	net.Conn
	ID   string
	data []byte
}

func (vc *virtualConn) Read(b []byte) (int, error) {
	n := copy(b, vc.data)
	vc.data = vc.data[n:]
	return n, nil
}

func (vc *virtualConn) Write(b []byte) (int, error) {
	ConnectionStore.Lock()
	ConnectionStore.Data[vc.ID] = append(ConnectionStore.Data[vc.ID], b...)
	ConnectionStore.Unlock()
	return len(b), nil
}

func (vc *virtualConn) Close() error {
	return nil
}

func (vc *virtualConn) LocalAddr() net.Addr {
	return &net.TCPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 0,
	}
}

func (vc *virtualConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 0,
	}
}

func (vc *virtualConn) SetDeadline(t time.Time) error {
	return nil
}

func (vc *virtualConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (vc *virtualConn) SetWriteDeadline(t time.Time) error {
	return nil
}

// StartConnectionMonitor 启动连接监视器，定期检查并清理断开的连接
func StartConnectionMonitor(interval time.Duration) {
	go func() {
		for {
			time.Sleep(interval)
			ConnectionStore.RLock()
			disconnected := []string{}

			for id, conn := range ConnectionStore.Connections {
				if conn == nil {
					disconnected = append(disconnected, id)
					continue
				}
				// 使用 SetReadDeadline 来测试连接是否还活跃
				conn.SetReadDeadline(time.Now().Add(time.Millisecond * 10))
				oneByte := make([]byte, 1)
				if _, err := conn.Read(oneByte); err != nil {
					if err, ok := err.(net.Error); ok && err.Timeout() {
						// Timeout means connection is still alive but no data was read
						conn.SetReadDeadline(time.Time{}) // Reset deadline
						continue
					}
					// Connection is not active
					disconnected = append(disconnected, id)
				}
				conn.SetReadDeadline(time.Time{}) // Reset deadline
			}

			ConnectionStore.RUnlock()

			if len(disconnected) > 0 {
				ConnectionStore.Lock()
				for _, id := range disconnected {
					delete(ConnectionStore.Connections, id)
					delete(ConnectionStore.Data, id)
					println("[*] delete connection: ", id)
				}
				ConnectionStore.Unlock()
			}
		}
	}()
}

// setupSocks5Server 设置并启动 SOCKS5 服务器
func setupSocks5Server() {
	conf := &socks5.Config{
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			reqID := uuid.New().String()
			println(reqID, ":", "Creating virtual connection")
			vc := &virtualConn{ID: reqID}
			ConnectionStore.Lock()
			ConnectionStore.Connections[reqID] = vc
			ConnectionStore.Unlock()
			return vc, nil
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
	if len(ConnectionStore.Connections) == 0 {
		ConnectionStore.RUnlock()
		http.Error(w, "No available connections", http.StatusServiceUnavailable)
		return
	}

	var chosenID string
	var chosenData []byte
	for id, data := range ConnectionStore.Data {
		chosenID = id
		chosenData = data
		break
	}
	ConnectionStore.RUnlock()

	w.Header().Set("X-Request-ID", chosenID)
	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := w.Write(chosenData); err != nil {
		log.Printf("Error sending data to client: %v", err)
		http.Error(w, "Failed to send data", http.StatusInternalServerError)
		return
	}
}

// handleTunnelResponse 处理隧道客户端的响应
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

	println("Get id - ", requestID, " response ,returning origin client...")
	println(string(body))
	ConnectionStore.RLock()
	conn, exists := ConnectionStore.Connections[requestID]
	ConnectionStore.RUnlock()
	if !exists {
		http.Error(w, "Request ID not found", http.StatusNotFound)
		return
	}

	_, err = conn.Write(body)
	if err != nil {
		log.Printf("Error sending response back to client: %v", err)
		http.Error(w, "Failed to send response", http.StatusInternalServerError)
		return
	}

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
	//StartConnectionMonitor(5 * time.Second) // 每10秒检查一次连接状态
	log.Fatal(server.ListenAndServeTLS("cert.crt", "key.key"))
}
