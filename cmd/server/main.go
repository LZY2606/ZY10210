// Command server runs the local dynamic-identification workshop over HTTP.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"dynid/internal/store"
	"dynid/internal/web"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:5550", "HTTP listen address")
	dbPath := flag.String("db", "dynid.db", "SQLite database path")
	flag.Parse()

	if dir := filepath.Dir(*dbPath); dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()

	srv, err := web.NewServer(st)
	if err != nil {
		log.Fatalf("初始化服务失败: %v", err)
	}
	fmt.Printf("动态辨识工场已启动: http://%s （数据库 %s）\n", *listen, *dbPath)
	if err := http.ListenAndServe(*listen, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
