package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
)

func handlerecv(w http.ResponseWriter, r *http.Request) {
	// 检查是不是POST请求
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is accepted", http.StatusMethodNotAllowed)
		return
	}

	// 读取POST数据
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}
	fmt.Printf("Received POST data: %s\n", string(body))

	// 通知客户端保持连接
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK) // 发送响应头

	// 持续发送数据
	count := 0
	for {
		data := fmt.Sprintf("Data chunk %d from server...\n", count)
		_, err := w.Write([]byte(data))
		if err != nil {
			fmt.Println("Error writing to client:", err)
			break
		}
		fmt.Printf("Sent: %s", data) // 打印已发送的数据
		w.(http.Flusher).Flush()     // 确保数据被发送

		count++
	}
}

func continuousRead(w http.ResponseWriter, r *http.Request, dataChan chan<- string) {
	defer close(dataChan) // 当读取完成时关闭 channel

	buffer := make([]byte, 1024)
	for {
		n, err := r.Body.Read(buffer)
		if n > 0 {
			println("data size: ", n, " bytes")
			dataChan <- string(buffer[:n]) // 发送数据到 channel
		}
		if err != nil {
			if err == io.EOF {
				continue // 即使遇到 EOF 也继续循环
			}
			continue // 忽略错误，继续读取
		}
	}
}

func handlesend(w http.ResponseWriter, r *http.Request) {
	//if r.Method != http.MethodPost {
	//	http.Error(w, "Only POST method is accepted", http.StatusMethodNotAllowed)
	//	return
	//}
	//
	//// 创建一个 channel 传递数据
	//dataChan := make(chan string)
	//
	//// 启动 goroutine 来读取数据
	//go continuousRead(w, r, dataChan)
	//
	//// 持续从 channel 读取数据
	//for data := range dataChan {
	//	fmt.Printf("Received: %s\n", data) // 打印数据
	//	// 可以在这里添加处理逻辑
	//}
	//
	//fmt.Println("Data stream ended or connection was closed.")
	//w.WriteHeader(http.StatusOK)
	//w.Write([]byte("Data processing completed.\n"))
	// 确保我们只处理POST请求
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	// 读取请求体
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// 打印接收到的数据
	fmt.Printf("Received data: %s\n", string(body))

	//// 可以回送数据或者确认信息到客户端
	//w.Write([]byte("Data received successfully"))
}

func main() {

	http.HandleFunc("/revc", handlerecv)
	http.HandleFunc("/send", handlesend)

	fmt.Println("Server started on :8081")
	if err := http.ListenAndServe(":8081", nil); err != nil {
		fmt.Printf("Server failed: %s\n", err)
	}
}
