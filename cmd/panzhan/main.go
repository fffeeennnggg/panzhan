// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"panzhan/internal/app"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	listen := flag.String("listen", "127.0.0.1:9867", "HTTP 监听地址")
	data := flag.String("data", "data", "持久化目录")
	root := flag.String("root", "downloads", "授权下载根目录")
	flag.Parse()
	a, err := app.New(*data, *root)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); a.Run(ctx) }()
	server := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fmt.Printf("盘栈已启动：http://%s\n", listener.Addr())
	if _, err = os.Stat(filepath.Join(*data, "setup-token.txt")); err == nil {
		fmt.Printf("首次配置：从 %s 读取安装令牌。\n", filepath.Join(*data, "setup-token.txt"))
	}
	select {
	case err = <-done:
		cancel()
	case <-ctx.Done():
	}
	shutdown, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	_ = server.Shutdown(shutdown)
	select {
	case <-workerDone:
	case <-shutdown.Done():
		return fmt.Errorf("任务关闭超时")
	}
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
