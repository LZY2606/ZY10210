// Command server 启动“动态辨识工场”本地服务。
package main

import (
	"flag"
	"log"
	"net/http"

	"dynidshop/internal/store"
	"dynidshop/internal/web"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:5550", "监听地址")
	dbPath := flag.String("db", "dynidshop.db", "SQLite 数据库路径")
	autoFixture := flag.Bool("auto-fixture", true, "数据库为空时自动导入固定 fixture")
	flag.Parse()

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()

	srv, err := web.NewServer(st)
	if err != nil {
		log.Fatalf("初始化服务失败: %v", err)
	}
	if *autoFixture {
		dss, _ := st.ListDatasets()
		if len(dss) == 0 {
			if _, err := srv.EnsureFixture(); err != nil {
				log.Printf("自动导入 fixture 失败: %v", err)
			} else {
				log.Printf("数据库为空，已自动导入固定 fixture")
			}
		}
	}

	log.Printf("动态辨识工场已启动：http://%s", *listen)
	if err := http.ListenAndServe(*listen, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
