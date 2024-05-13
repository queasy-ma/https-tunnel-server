package main

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Connection struct {
	conn       net.Conn
	target     string
	port       int
	isDomain   int
	isConnect  bool
	createTime time.Time // 添加时间字段
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
	// 定义命令行参数
	socks5Port := flag.Int("socks5-port", 1080, "Port to listen on for SOCKS5 server")
	httpPort := flag.Int("http-port", 8089, "Port to listen on for HTTP server")

	// 定义短参数名
	sp := flag.Int("sp", 0, "Port to listen on for SOCKS5 server")
	hp := flag.Int("hp", 0, "Port to listen on for HTTP server")

	flag.Parse() // 解析命令行参数

	// 检查是否使用了短参数名，并根据需要更新端口值
	if *sp != 0 {
		*socks5Port = *sp
	}
	if *hp != 0 {
		*httpPort = *hp
	}
	go checkAndCloseConnections()
	go startSocks5Server(*socks5Port)
	startHTTPServer(*httpPort)
}

func checkAndCloseConnections() {
	for {
		time.Sleep(5 * time.Second) // 每5秒执行一次

		storeLock.Lock() // 加写锁
		for key, conn := range connStore {
			if time.Since(conn.createTime).Seconds() > 5 && !conn.isConnect { // 检查连接时间是否超过5秒
				println("[", key, "]", "client no response, close...")
				conn.conn.Close()      // 关闭连接
				delete(connStore, key) // 从map中移除
			}
		}
		storeLock.Unlock() // 释放写锁
	}
}

func startSocks5Server(port int) {
	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Println("Failed to set up listener:", err)
		os.Exit(1)
	}
	fmt.Println("SOCKS5 server listening on ", addr)

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
	clientID := uuid.New().String()
	//clientID := "8688bb89-5ace-48b1-a158-dfe154429b27"
	fmt.Printf("handle connect %v ...\n", conn.RemoteAddr())

	connection := &Connection{conn: conn}
	println("Get connect id:", clientID)

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
		connection.target = targetAddr.IP.String()
		connection.port = targetAddr.Port
		connection.isDomain = 0
	case 0x03: // Domain name
		domainLength := buffer[4]
		domainName := string(buffer[5 : 5+domainLength])
		targetAddr.Port = int(binary.BigEndian.Uint16(buffer[5+domainLength : 5+domainLength+2]))
		//resolvedIPs, err := net.LookupIP(domainName)
		//if err != nil {
		//	fmt.Println("Domain name resolution failed:", err)
		//	return
		//}
		targetAddr.IP = net.ParseIP("127.0.0.1")
		connection.target = domainName
		connection.port = targetAddr.Port
		connection.isDomain = 1
	default:
		fmt.Println("Unsupported address type")
		return
	}
	connection.isConnect = false
	connection.createTime = time.Now()
	storeLock.Lock()
	connStore[clientID] = connection
	storeLock.Unlock()
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

func startHTTPServer(port int) {
	http.HandleFunc("/recv", handleRecv)
	http.HandleFunc("/send", handleSend)
	http.HandleFunc("/info", handleInfo)
	http.HandleFunc("/close", handleClose)
	addr := fmt.Sprintf(":%d", port)
	fmt.Println("HTTPS server listening on ", addr)
	if err := http.ListenAndServeTLS(addr, "cert.crt", "key.key", nil); err != nil {
		fmt.Println("Failed to start HTTPS server:", err)
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
		//http.Error(w, "\nMethod not allowed. from /recv", http.StatusMethodNotAllowed)
		return
	}

	clientID := r.URL.Query().Get("client_id")
	if clientID == "" {
		//http.Error(w, "Client ID is required", http.StatusBadRequest)
		return
	}

	storeLock.RLock()
	connection, ok := connStore[clientID]
	storeLock.RUnlock()

	if !ok {
		println("No active connection for this client")
		w.Header().Set("Connectionstatus", "close")
		return
	}
	if !connection.isConnect {
		connection.isConnect = true
	}
	connection.conn.SetReadDeadline(time.Now().Add(time.Millisecond * 2))
	buffer := make([]byte, 1024)
	var totalData []byte
	totalBytesRead := 0
	for {
		bytesRead, err := connection.conn.Read(buffer)
		if err != nil {
			if err == io.EOF {
				// 客户端已关闭连接
				println("Client has closed the connection", http.StatusGone)
				closeByUUID(clientID)
				w.Header().Set("Connectionstatus", "close")
			} else {
				// 使用类型断言检查错误是否实现了net.Error接口
				var nErr net.Error
				if errors.As(err, &nErr) && nErr.Timeout() {
					// 超时模拟非阻塞读取
				}
			}
			break
		}

		// 将读取的数据追加到总数据中
		totalData = append(totalData, buffer[:bytesRead]...)
		totalBytesRead += bytesRead
		// 可以考虑在这里检查 totalData 的长度或其他退出条件
	}
	if totalBytesRead == 0 {
		return
	}
	//encoded := base64.StdEncoding.EncodeToString(buffer[:bytesRead])
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(totalData[:totalBytesRead])
	fmt.Printf("Data sent to tunnel client, %d bytes.\n", totalBytesRead)

}

func handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "\nMethod not allowed. from /send", http.StatusMethodNotAllowed)
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

	if !connection.isConnect {
		connection.isConnect = true
	}

	data, err := ioutil.ReadAll(r.Body)
	if err != nil {
		println("Failed to read data", http.StatusInternalServerError)
		return
	}
	// 解码数据
	//decoded, err := base64.StdEncoding.DecodeString(string(data))
	//if err != nil {
	//	fmt.Println("Error decoding:", err)
	//	return
	//}
	_, err = connection.conn.Write(data)
	if err != nil {
		println("Failed to send data to the SOCKS5 connection", http.StatusInternalServerError)
		return
	}

	fmt.Println("Data sent to original client", len(data), " bytes.")
	//w.WriteHeader(http.StatusOK)
}

func handleInfo(w http.ResponseWriter, r *http.Request) {
	storeLock.RLock()
	defer storeLock.RUnlock()

	var parts []string
	for uuid, conn := range connStore {
		if !conn.isConnect { // 只选择 isConnect 为 false 的连接
			part := fmt.Sprintf("%d;%s;%s;%d", conn.isDomain, uuid, conn.target, conn.port)
			parts = append(parts, base64.StdEncoding.EncodeToString([]byte(part)))
		}
	}

	// 拼接所有部分并使用 base64 编码
	response := strings.Join(parts, "|")
	//encodedResponse := base64.StdEncoding.EncodeToString([]byte(response))
	//println(encodedResponse)
	// 设置 HTTP header
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(response))
}

func closeByUUID(clientID string) {
	// Attempt to find and close the connection
	storeLock.Lock()
	if conn, ok := connStore[clientID]; ok {
		if conn.conn != nil {
			conn.conn.Close()
		}
		delete(connStore, clientID)
		println("Connection closed and delete for client_id: ", clientID)
	} else {
		println("No connection found for client_id: ", clientID)
	}
	storeLock.Unlock()
}

func handleClose(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	query := r.URL.Query()
	clientID := query.Get("client_id")
	if clientID == "" {
		http.Error(w, "\nMissing client_id", http.StatusBadRequest)
		return
	}
	closeByUUID(clientID)
}
