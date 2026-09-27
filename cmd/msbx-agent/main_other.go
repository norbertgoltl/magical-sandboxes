//go:build !linux

package main

import "fmt"

func main() {
	fmt.Println("msbx-agent is a Linux guest binary; build with GOOS=linux GOARCH=arm64")
}
