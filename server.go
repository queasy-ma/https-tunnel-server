package main

import (
	"crypto/tls"
	"golang.org/x/net/http2"
	"net/http"
	"time"
)

func main() {
	// 设置 HTTPS 服务器
	mux := http.NewServeMux()
	mux.HandleFunc("/tunnel", handleTunnel)

	server := &http.Server{
		Addr:    ":443", // 通常 HTTPS 使用 443 端口
		Handler: mux,
		TLSConfig: &tls.Config{
			MinVersion:               tls.VersionTLS12,
			PreferServerCipherSuites: true,
		},
	}

	// 启用 HTTP/2
	http2.ConfigureServer(server, nil)

	// 注意：这里使用的是自签名证书，实际部署时应使用有效的证书
	err := server.ListenAndServeTLS("cert.crt", "key.key")
	if err != nil {
		panic(err)
	}
}

func handleTunnel(w http.ResponseWriter, r *http.Request) {
	// 发送响应以建立连接
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("HTTP/2 Tunnel Established"))

	// 设置长连接
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	// 心跳循环，发送心跳包维持连接
	ticker := time.NewTicker(3 * time.Second) // 每3秒发送一次
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			w.Write([]byte("ping\n")) // 发送心跳包
			flusher.Flush()
		case <-r.Context().Done():
			return // 如果客户端关闭连接，退出循环
		}
	}
}
