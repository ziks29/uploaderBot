//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func startApp() {
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
}
