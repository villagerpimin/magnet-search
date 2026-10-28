// Command msearch 是磁力搜索聚合命令行工具。
package main

import (
	"fmt"
	"os"
)

// version 是程序版本号。
const version = "2.1.1"

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			os.Exit(runServe(args[1:]))
		case "check":
			os.Exit(runCheck(args[1:]))
		case "engines":
			os.Exit(runEngines())
		case "version", "--version", "-v":
			fmt.Println("msearch " + version)
			os.Exit(0)
		case "help", "--help", "-h":
			usage()
			os.Exit(0)
		}
	}
	os.Exit(runSearch(args))
}
