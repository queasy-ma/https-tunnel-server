package main

import (
	"crypto/tls"
	"golang.org/x/net/http2"
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 主动推送资源
		if pusher, ok := w.(http.Pusher); ok {
			// 这里可以根据具体逻辑选择推送哪些资源
			if err := pusher.Push("/extra/data", nil); err != nil {
				log.Printf("Failed to push: %v", err)
			}
		}
		// 正常响应请求
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("Response to main request"))
	})

	mux.HandleFunc("/extra/data", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("This is pushed data!"))
	})

	server := &http.Server{
		Addr:    ":8443",
		Handler: mux,
		TLSConfig: &tls.Config{
			NextProtos: []string{http2.NextProtoTLS},
		},
	}

	http2.ConfigureServer(server, &http2.Server{})
	log.Println("Serving on https://localhost:8443")
	log.Fatal(server.ListenAndServeTLS("server.crt", "server.key"))
}
