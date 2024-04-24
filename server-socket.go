package main

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

type Connection struct {
	conn net.Conn
}

var (
	connStore = make(map[string]*Connection)
	storeLock = sync.RWMutex{}
)

var (
	buffer1  = make([]byte, 1024)
	dataChan = make(chan []byte)
	errChan  = make(chan error)
)

func main() {
	go startSocks5Server()
	startHTTPServer()
}

func startSocks5Server() {
	listener, err := net.Listen("tcp", ":1080")
	if err != nil {
		fmt.Println("Failed to set up listener:", err)
		os.Exit(1)
	}
	fmt.Println("SOCKS5 server listening on :1080...")

	for {
		conn, err := listener.Accept()
		if err != nil {
			fmt.Println("Failed to accept connection:", err)
			continue
		}
		go handleSocks5Connection(conn)
	}
}

func handleSocks5Connection(conn net.Conn) {
	//clientID := uuid.New().String()
	clientID := "8688bb89-5ace-48b1-a158-dfe154429b27"
	fmt.Printf("handle connect %v ...\n", conn.RemoteAddr())

	connection := &Connection{conn: conn}
	println("Get connect id:", clientID)
	storeLock.Lock()
	connStore[clientID] = connection
	storeLock.Unlock()

	reader := bufio.NewReader(conn)
	buffer := make([]byte, 1024)

	// Initial handshake process
	_, err := reader.Read(buffer)
	if err != nil {
		fmt.Println("Error reading greeting from client:", err)
		return
	}
	_, err = conn.Write([]byte{0x05, 0x00}) // SOCKS5 version and no authentication required
	if err != nil {
		fmt.Println("Fail to send no-authentication response:", err)
		return
	}

	// Connection request process
	_, err = reader.Read(buffer)
	if err != nil {
		fmt.Println("Error reading connection request:", err)
		return
	}
	if buffer[1] != 0x01 { // CONNECT command
		fmt.Println("Unsupported command")
		return
	}

	// Address resolution
	addressType := buffer[3]
	var targetAddr net.TCPAddr
	switch addressType {
	case 0x01: // IPv4
		targetAddr.IP = net.IP(buffer[4:8])
		targetAddr.Port = int(binary.BigEndian.Uint16(buffer[8:10]))
	case 0x03: // Domain name
		domainLength := buffer[4]
		domainName := string(buffer[5 : 5+domainLength])
		targetAddr.Port = int(binary.BigEndian.Uint16(buffer[5+domainLength : 5+domainLength+2]))
		resolvedIPs, err := net.LookupIP(domainName)
		if err != nil {
			fmt.Println("Domain name resolution failed:", err)
			return
		}
		targetAddr.IP = resolvedIPs[0]
	default:
		fmt.Println("Unsupported address type")
		return
	}
	// 发送成功响应给客户端
	err = sendSuccessResponse(connection.conn, targetAddr)
	if err != nil {
		fmt.Println("Failed to send success response:", err)
		return
	}
	fmt.Printf("Resolved IP: %s, Port: %d\n", targetAddr.IP, targetAddr.Port)
	//go func() {
	//	bytesRead, err := connection.conn.Read(buffer1)
	//	if err != nil {
	//		errChan <- err
	//		return
	//	}
	//	dataChan <- buffer[:bytesRead]
	//}()
	//targetAddr := &net.TCPAddr{
	//	IP:   net.IPv4(198, 18, 0, 63),
	//	Port: 80,
	//}

}

func startHTTPServer() {
	http.HandleFunc("/recv", handleRecv)
	http.HandleFunc("/send", handleSend)
	fmt.Println("HTTP server listening on :8089...")
	if err := http.ListenAndServe(":8089", nil); err != nil {
		fmt.Println("Failed to start HTTP server:", err)
	}
}

func sendSuccessResponse(conn net.Conn, addr net.TCPAddr) error {
	var response [10]byte
	response[0] = 0x05                                            // SOCKS5版本
	response[1] = 0x00                                            // 成功响应
	response[2] = 0x00                                            // 保留字段
	response[3] = 0x01                                            // 地址类型，IPv4
	copy(response[4:8], addr.IP.To4())                            // IP地址
	binary.BigEndian.PutUint16(response[8:10], uint16(addr.Port)) // 端口

	_, err := conn.Write(response[:])
	return err
}

func handleRecv(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		http.Error(w, "Client ID is required", http.StatusBadRequest)
		return
	}

	storeLock.RLock()
	connection, ok := connStore[clientID]
	storeLock.RUnlock()

	if !ok {
		http.Error(w, "No active connection for this client", http.StatusNotFound)
		return
	}
	connection.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 200))
	buffer := make([]byte, 1024)
	bytesRead, err := connection.conn.Read(buffer)
	if err != nil {
		if err == io.EOF {
			// Client has closed the connection
			println("Client has closed the connection", http.StatusGone)
		} else {
			// Some other error
			println(err.Error(), http.StatusInternalServerError)
		}
		return
	}
	encoded := base64.StdEncoding.EncodeToString(buffer[:bytesRead])
	w.Write([]byte(encoded))
	fmt.Printf("Data sent to tunnel client, %d bytes.\n", bytesRead)

}

func handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		http.Error(w, "Client ID is required", http.StatusBadRequest)
		return
	}

	storeLock.RLock()
	connection, ok := connStore[clientID]
	storeLock.RUnlock()

	if !ok {
		http.Error(w, "No active connection for this client", http.StatusNotFound)
		return
	}

	data, err := ioutil.ReadAll(r.Body)
	if err != nil {
		println("Failed to read data", http.StatusInternalServerError)
		return
	}
	// 解码数据
	decoded, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		fmt.Println("Error decoding:", err)
		return
	}
	_, err = connection.conn.Write(decoded)
	if err != nil {
		println("Failed to send data to the SOCKS5 connection", http.StatusInternalServerError)
		return
	}

	fmt.Println("Data successfully sent ", len(decoded), " bytes to the original client.")
	w.WriteHeader(http.StatusOK)
}
